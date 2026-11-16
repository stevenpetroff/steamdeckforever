package chatlog

import (
	"os"
	"strings"
	"testing"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

type want struct {
	channel     protocol.Channel
	channelName string
	sender      string
	realm       string
	text        string
	outgoing    bool
}

// fixtureWant lists the expected parse of each line of testdata/forever-sample.log, in order.
var fixtureWant = []want{
	{channel: protocol.ChannelSystem, text: "You have joined the guild."},
	{channel: protocol.ChannelSystem, text: "Bob has come online."},
	{channel: protocol.ChannelNumbered, channelName: "1. General - Elwynn Forest", sender: "Bob", text: "anyone know where the kobolds are?"},
	{channel: protocol.ChannelNumbered, channelName: "2. Trade - City", sender: "Bob", text: "WTS [Linen Cloth] x20, 5s each"},
	{channel: protocol.ChannelSay, sender: "Bob", text: "hi"},
	{channel: protocol.ChannelYell, sender: "Bob", text: "HELP ME KILL HOGGER"},
	{channel: protocol.ChannelWhisper, sender: "Thrall", realm: "Doomhowl", text: "hey"},
	{channel: protocol.ChannelWhisper, sender: "Jaina", text: "hi"},
	{channel: protocol.ChannelWhisper, sender: "Thrall", realm: "Doomhowl", text: "hi", outgoing: true},
	{channel: protocol.ChannelParty, sender: "Bob", text: "pull when ready"},
	{channel: protocol.ChannelPartyLeader, sender: "Jaina", text: "wait for mana"},
	{channel: protocol.ChannelRaid, sender: "Bob", text: "on my way"},
	{channel: protocol.ChannelRaidLeader, sender: "Jaina", realm: "Doomhowl", text: "stack on me"},
	{channel: protocol.ChannelRaidWarning, sender: "Jaina", text: "SPREAD OUT"},
	{channel: protocol.ChannelGuild, sender: "Bob", text: "note: [Linen Cloth] drops from [Defias Thug]: ask me"},
	{channel: protocol.ChannelOfficer, sender: "Jaina", text: "promote Bob?"},
	{channel: protocol.ChannelSystem, text: "Bob waves at you."},
	{channel: protocol.ChannelNumbered, channelName: "4. LookingForGroup", sender: "Thrall", realm: "Doomhowl", text: "LF2M Deadmines"},
	{channel: protocol.ChannelWhisper, sender: "Jaina", text: "brb", outgoing: true},
	{channel: protocol.ChannelSay, sender: "Bob", text: "crlf line"},
	{channel: protocol.ChannelSystem, text: "Your skill in Tailoring has increased to 51."},
}

func check(t *testing.T, line string, got protocol.Message, w want) {
	t.Helper()
	if got.Channel != w.channel || got.ChannelName != w.channelName || got.Sender != w.sender ||
		got.SenderRealm != w.realm || got.Text != w.text || got.Outgoing != w.outgoing {
		t.Errorf("Parse(%q)\n got  %+v\n want %+v", line, got, w)
	}
	if got.ID != "" {
		t.Errorf("Parse(%q) set ID %q; the hub assigns IDs", line, got.ID)
	}
}

func TestParseFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/forever-sample.log")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(fixtureWant) {
		t.Fatalf("fixture has %d lines, table has %d", len(lines), len(fixtureWant))
	}
	sawCR := false
	for i, line := range lines {
		if strings.HasSuffix(line, "\r") {
			sawCR = true
		}
		got, ok := Parse(line)
		if !ok {
			t.Errorf("line %d: Parse(%q) returned false", i+1, line)
			continue
		}
		if got.Time != line[:18] {
			t.Errorf("line %d: Time = %q, want %q", i+1, got.Time, line[:18])
		}
		check(t, line, got, fixtureWant[i])
	}
	if !sawCR {
		t.Error("fixture must contain a CRLF line")
	}
}

func TestParseCases(t *testing.T) {
	cases := []struct {
		name string
		line string
		want want
	}{
		{"outgoing whisper with realm", "11/14 18:02:11.204  To [Thrall-Doomhowl]: hi",
			want{channel: protocol.ChannelWhisper, sender: "Thrall", realm: "Doomhowl", text: "hi", outgoing: true}},
		{"trade channel name", "11/14 18:02:11.204  [2. Trade - City] [Bob]: WTS",
			want{channel: protocol.ChannelNumbered, channelName: "2. Trade - City", sender: "Bob", text: "WTS"}},
		{"unknown shape keeps full text", "11/14 18:02:11.204  [Weird] thing: happened [here]",
			want{channel: protocol.ChannelSystem, text: "[Weird] thing: happened [here]"}},
		{"crlf stripped", "1/2 3:04:05.678  [Bob] says: yo\r",
			want{channel: protocol.ChannelSay, sender: "Bob", text: "yo"}},
		{"invalid utf8 replaced", "11/14 18:02:11.204  [Bob] says: a\xffb",
			want{channel: protocol.ChannelSay, sender: "Bob", text: "a�b"}},
		{"realm only on dash", "11/14 18:02:11.204  [Guild] [Jaina-Doom-howl]: x",
			want{channel: protocol.ChannelGuild, sender: "Jaina", realm: "Doom-howl", text: "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Parse(c.line)
			if !ok {
				t.Fatalf("Parse(%q) returned false", c.line)
			}
			check(t, c.line, got, c.want)
		})
	}
}

func TestParseRejects(t *testing.T) {
	for _, line := range []string{"", "   ", "\r", "\t \r", "no timestamp here", "11/14 18:02:11  missing millis", "11/14 18:02:11.204 one space"} {
		if m, ok := Parse(line); ok {
			t.Errorf("Parse(%q) = %+v, true; want false", line, m)
		}
	}
}
