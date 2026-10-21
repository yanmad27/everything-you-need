package admin

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"everything-you-need/m/services/convo"
)

type spy struct {
	fixture
	chatID    string
	platform  string
	timelines int
	page      convo.Page
}

const oddID = "a b+c%d&e=f/é"

func (s *spy) ListChats(context.Context, convo.ChatFilter, convo.Page) ([]convo.ChatSummary, int, error) {
	return []convo.ChatSummary{{Platform: "zalo", ChatID: oddID, ChatType: "GROUP"}}, 1, nil
}

func (s *spy) Timeline(ctx context.Context, platform, chatID string, p convo.Page) ([]convo.TimelineItem, int, error) {
	s.timelines++
	s.platform, s.chatID, s.page = platform, chatID, p
	return s.fixture.Timeline(ctx, platform, chatID, p)
}

func TestChatLinkRoundTripsAndTimelineQueriedOnce(t *testing.T) {
	sp := &spy{}
	h, err := New(Config{Username: "owner", PasswordHash: testHash}, sp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ck := loginSession(t, h)
	list := do(h, "GET", "/admin/chats", nil, ck).Body.String()
	m := regexp.MustCompile(`href="(/admin/chat\?[^"]+)"`).FindStringSubmatch(list)
	if m == nil {
		t.Fatalf("no link in %s", list)
	}
	href := html.UnescapeString(m[1])
	if strings.Contains(href, " ") || strings.Contains(href, "+c%d") {
		t.Errorf("link not encoded: %s", href)
	}
	if u, err := url.Parse(href); err != nil || u.Query().Get("id") != oddID {
		t.Fatalf("link id = %q err=%v", u.Query().Get("id"), err)
	}
	if w := do(h, "GET", href, nil, ck); w.Code != 200 {
		t.Fatalf("open: %d", w.Code)
	}
	if sp.chatID != oddID || sp.platform != "zalo" {
		t.Errorf("timeline got %q/%q", sp.platform, sp.chatID)
	}
	if sp.timelines != 1 || !sp.page.Desc || sp.page.Offset != 0 {
		t.Errorf("timeline calls=%d page=%+v", sp.timelines, sp.page)
	}
	sp.timelines = 0
	do(h, "GET", href+"&page=2", nil, ck)
	if sp.timelines != 1 || sp.page.Offset != timelinePage {
		t.Errorf("page 2: calls=%d page=%+v", sp.timelines, sp.page)
	}
}

func TestNoInlineStyleAttributes(t *testing.T) {
	h, _ := newH(t)
	ck := loginSession(t, h)
	for _, p := range []string{"/admin/tools", "/admin/chats", "/admin/users", "/admin/chat?platform=zalo&id=c1"} {
		if strings.Contains(do(h, "GET", p, nil, ck).Body.String(), ` style="`) {
			t.Errorf("%s has an inline style attribute", p)
		}
	}
}
