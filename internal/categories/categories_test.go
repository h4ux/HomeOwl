package categories

import "testing"

func TestCategorize(t *testing.T) {
	cases := []struct {
		root, tracker string
		rules         []Rule
		want          string
	}{
		{"tiktokv.com", "", nil, "Social"},
		{"googlevideo.com", "", nil, "Video & streaming"},
		{"router.lan", "", nil, "Local network"},
		{"somecasino-site.com", "", nil, "Gambling"},
		{"unknown-example.org", "", nil, "Uncategorized"},
		{"unknown-example.org", "SomeTracker", nil, "Ads & trackers"},
		{"tiktokv.com", "", []Rule{{"tiktokv.com", "Kids"}}, "Kids"},
		{"cdn.mystuff.io", "", []Rule{{"~mystuff", "Work & productivity"}}, "Work & productivity"},
	}
	for _, c := range cases {
		if got, _ := Categorize(c.root, "", c.tracker, c.rules); got != c.want {
			t.Errorf("%s: got %s, want %s", c.root, got, c.want)
		}
	}
}
