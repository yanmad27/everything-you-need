package news

import "testing"

func TestParseRankResponse(t *testing.T) {
	candidates := []FeedItem{
		{Title: "First", URL: "https://a.com/1"},
		{Title: "Second", URL: "https://b.com/2"},
	}
	// Gemini's outer envelope wrapping the JSON array it produced.
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"[{\"index\":1,\"en_title\":\"Second EN\",\"en_summary\":\"sum\",\"vi_title\":\"Second VI\",\"vi_summary\":\"tom tat\"},{\"index\":0,\"en_title\":\"First EN\",\"en_summary\":\"\",\"vi_title\":\"\",\"vi_summary\":\"\"}]"}]}}]}`)

	ranked, err := parseRankResponse(raw, candidates)
	if err != nil {
		t.Fatalf("parseRankResponse: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked, got %d", len(ranked))
	}
	if ranked[0].URL != "https://b.com/2" {
		t.Errorf("first ranked URL = %q, want b.com (index 1)", ranked[0].URL)
	}
	if ranked[0].EnglishTitle != "Second EN" {
		t.Errorf("first ranked en_title = %q", ranked[0].EnglishTitle)
	}
	// second item had empty titles → should fall back to candidate title
	if ranked[1].EnglishTitle != "First EN" {
		t.Errorf("second ranked en_title = %q", ranked[1].EnglishTitle)
	}
}

func TestParseRankResponseDropsBadIndex(t *testing.T) {
	candidates := []FeedItem{{Title: "Only", URL: "https://a.com/1"}}
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"[{\"index\":99,\"en_title\":\"Ghost\"},{\"index\":0,\"en_title\":\"Real\"}]"}]}}]}`)
	ranked, err := parseRankResponse(raw, candidates)
	if err != nil {
		t.Fatalf("parseRankResponse: %v", err)
	}
	if len(ranked) != 1 || ranked[0].EnglishTitle != "Real" {
		t.Fatalf("expected only the valid-index item, got %+v", ranked)
	}
}
