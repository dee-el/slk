package messages

import "testing"

func TestFlattenMrkdwn(t *testing.T) {
	resolveUser := func(id string) (string, bool) {
		if id == "U081FJDPAHF" {
			return "ayush", true
		}
		return "", false
	}
	resolveChannel := func(id string) (string, bool) {
		if id == "C123" {
			return "general", true
		}
		return "", false
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"user mention with label", "hi <@U0AQH6B2ULX|Ayush>!", "hi @Ayush!"},
		{"user mention resolved", "hi <@U081FJDPAHF>", "hi @ayush"},
		{"user mention unknown falls back to ID", "hi <@U0AQH6B2ULX>", "hi @U0AQH6B2ULX"},
		{"channel ref with name", "see <#C999|ops>", "see #ops"},
		{"channel ref resolved", "see <#C123>", "see #general"},
		{"channel ref unknown falls back to ID", "see <#C999>", "see #C999"},
		{"link with label", "ticket <https://linear.app/x/issue/ABC-1|ABC-1>", "ticket ABC-1"},
		{"bare link", "see <https://linear.app/x>", "see https://linear.app/x"},
		{"mailto drops scheme", "mail <mailto:a@b.co|a@b.co>", "mail a@b.co"},
		{"here", "<!here> deploy", "@here deploy"},
		{"channel broadcast", "<!channel> deploy", "@channel deploy"},
		{"everyone", "<!everyone> deploy", "@everyone deploy"},
		{"special with label", "<!here|@here> deploy", "@here deploy"},
		{"date token uses fallback", "due <!date^1700000000^{date}|Nov 14>", "due Nov 14"},
		{"usergroup mention with label", "ping <!subteam^S0AB12CD3|@eng> please", "ping @eng please"},
		{"usergroup label without @ gets one", "ping <!subteam^S0AB12CD3|eng>", "ping @eng"},
		{"usergroup mention unlabeled falls back", "ping <!subteam^S0AB12CD3>", "ping @group"},
		{"html entities decoded", "a &amp; b &lt;c&gt;", "a & b <c>"},
		{"plain text untouched", "just words", "just words"},
		{"multiple entities", "<@U081FJDPAHF> in <#C123>: <https://x.co|x>", "@ayush in #general: x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FlattenMrkdwn(tt.in, resolveUser, resolveChannel); got != tt.want {
				t.Errorf("FlattenMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFlattenMrkdwnNilResolvers(t *testing.T) {
	got := FlattenMrkdwn("<@U1> <#C1>", nil, nil)
	if got != "@U1 #C1" {
		t.Errorf("got %q, want %q", got, "@U1 #C1")
	}
}

func TestFlattenMrkdwnWithUserGroups(t *testing.T) {
	resolveUser := func(id string) (string, bool) { return "", false }
	resolveChannel := func(id string) (string, bool) { return "", false }

	tests := []struct {
		name       string
		in         string
		userGroups map[string]string
		want       string
	}{
		{"bare token uses map", "ping <!subteam^S123>", map[string]string{"S123": "eng"}, "ping @eng"},
		{"label wins over map", "ping <!subteam^S123|@platform>", map[string]string{"S123": "eng"}, "ping @platform"},
		{"map handle keeps one at", "ping <!subteam^S123>", map[string]string{"S123": "@@eng"}, "ping @eng"},
		{"unknown falls back", "ping <!subteam^S999>", nil, "ping @group"},
		{"escaped token stays literal", "&lt;!subteam^S123|@platform&gt;", map[string]string{"S123": "eng"}, "<!subteam^S123|@platform>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FlattenMrkdwnWithUserGroups(tt.in, resolveUser, resolveChannel, tt.userGroups)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
