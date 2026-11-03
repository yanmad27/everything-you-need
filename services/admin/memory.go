package admin

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"everything-you-need/m/services/memory"
)

// detailAuditor is implemented by *convo.Store: an audit row with an actor and
// a short detail. Without it the detail is folded into the event name.
type detailAuditor interface {
	RecordAdminAuditDetail(ctx context.Context, event, ip, detail, actor string) error
}

type memNotice struct{ Kind, Text string }

func memoryNotice(code string) *memNotice {
	switch code {
	case "deleted":
		return &memNotice{"ok", "Đã xoá 1 ký ức."}
	case "gone":
		return &memNotice{"ok", "Ký ức này không còn nữa."}
	}
	if n, ok := strings.CutPrefix(code, "deleted_all_"); ok {
		if c, err := strconv.Atoi(n); err == nil && c >= 0 && c < 1e7 {
			return &memNotice{"ok", fmt.Sprintf("Đã xoá toàn bộ bộ nhớ của người này (%d ký ức).", c)}
		}
	}
	return nil
}

// idToken keeps an id printable and short for the audit trail.
func idToken(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return r
		}
		return '_'
	}, s)
}

func (h *Handler) auditMemory(r *http.Request, event, platform, userID string, count int) {
	detail := fmt.Sprintf("%s/%s n=%d", idToken(platform), idToken(userID), count)
	ip := clientIP(r)
	if da, ok := h.creds.(detailAuditor); ok && h.creds != nil {
		if err := da.RecordAdminAuditDetail(r.Context(), event, ip, detail, h.username); err != nil {
			log.Printf("admin audit: write failed event=%s", event)
		}
		log.Printf("admin audit: event=%s ip=%s actor=%s %s", event, ip, h.username, detail)
		return
	}
	h.audit(r, event)
	log.Printf("admin audit: event=%s actor=%s %s", event, h.username, detail)
}

// memoryDelete handles POST /admin/memory/delete: delete one memory (id) or
// every memory (all=1) of one person. CSRF and the admin password are both
// required; nothing is deleted otherwise. GET never deletes.
func (h *Handler) memoryDelete(w http.ResponseWriter, r *http.Request, s *session) {
	if h.memory == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	platform, userID := clip(r.PostForm.Get("platform"), 20), clip(r.PostForm.Get("id"), 100)
	all := r.PostForm.Get("all") == "1"
	memID, err := strconv.ParseInt(r.PostForm.Get("memory_id"), 10, 64)
	if platform == "" || userID == "" || (!all && (err != nil || memID <= 0)) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	back := "/admin/memory?" + url.Values{"platform": {platform}, "id": {userID}}.Encode()
	if ok, status, msg := h.reauth(r, s, r.PostForm.Get("admin_password")); !ok {
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "900")
		}
		if status == http.StatusBadRequest {
			status = http.StatusForbidden // wrong or missing password: refuse, nothing deleted
		}
		http.Error(w, msg, status)
		return
	}
	sc := memory.Scope{Platform: platform, UserID: userID}
	if all {
		n, err := h.memory.DeleteAll(r.Context(), sc)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.auditMemory(r, "memory_delete_all", platform, userID, n)
		http.Redirect(w, r, back+"&flash=deleted_all_"+strconv.Itoa(n), http.StatusSeeOther)
		return
	}
	switch err := h.memory.DeleteID(r.Context(), sc, memID); {
	case err == nil:
		h.auditMemory(r, "memory_delete", platform, userID, 1)
		http.Redirect(w, r, back+"&flash=deleted", http.StatusSeeOther)
	case errors.Is(err, memory.ErrNotFound):
		http.Redirect(w, r, back+"&flash=gone", http.StatusSeeOther)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
