// Package chatlog reads WoW's WoWChatLog.txt: Parse turns one line into a
// protocol.Message and Tail follows the file as the game appends to it.
package chatlog

import (
	"regexp"
	"strings"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

var (
	// "M/D HH:MM:SS.mmm" followed by exactly two spaces.
	lineRe = regexp.MustCompile(`^(\d{1,2}/\d{1,2} \d{1,2}:\d{2}:\d{2}\.\d{3})  (.*)$`)

	// Names are a single token: no spaces or brackets. A realm follows '-'.
	speechRe   = regexp.MustCompile(`^\[([^\[\]\s]+)\] (says|yells|whispers): (.*)$`)
	outWhispRe = regexp.MustCompile(`^To \[([^\[\]\s]+)\]: (.*)$`)
	groupRe    = regexp.MustCompile(`^\[(Party|Party Leader|Raid|Raid Leader|Raid Warning|Guild|Officer)\] \[([^\[\]\s]+)\]: (.*)$`)
	numberedRe = regexp.MustCompile(`^\[(\d+\. [^\[\]]+)\] \[([^\[\]\s]+)\]: (.*)$`)
)

var speechChannels = map[string]protocol.Channel{
	"says":     protocol.ChannelSay,
	"yells":    protocol.ChannelYell,
	"whispers": protocol.ChannelWhisper,
}

var groupChannels = map[string]protocol.Channel{
	"Party":        protocol.ChannelParty,
	"Party Leader": protocol.ChannelPartyLeader,
	"Raid":         protocol.ChannelRaid,
	"Raid Leader":  protocol.ChannelRaidLeader,
	"Raid Warning": protocol.ChannelRaidWarning,
	"Guild":        protocol.ChannelGuild,
	"Officer":      protocol.ChannelOfficer,
}

// Parse turns one chat log line into a Message. It returns false only for
// blank lines and lines without a valid timestamp prefix. Lines whose text
// matches no known channel shape become system messages with the full text.
// Emotes can't be told apart from system lines, so they parse as system.
// The ID is left empty for the hub to assign.
func Parse(line string) (protocol.Message, bool) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return protocol.Message{}, false
	}
	line = strings.ToValidUTF8(line, "�")
	m := lineRe.FindStringSubmatch(line)
	if m == nil || strings.TrimSpace(m[2]) == "" {
		return protocol.Message{}, false
	}
	msg := protocol.Message{Time: m[1]}
	text := m[2]

	switch {
	case matchInto(speechRe, text, func(g []string) {
		msg.Channel = speechChannels[g[2]]
		setSender(&msg, g[1])
		msg.Text = g[3]
	}):
	case matchInto(outWhispRe, text, func(g []string) {
		msg.Channel = protocol.ChannelWhisper
		msg.Outgoing = true
		setSender(&msg, g[1])
		msg.Text = g[2]
	}):
	case matchInto(groupRe, text, func(g []string) {
		msg.Channel = groupChannels[g[1]]
		setSender(&msg, g[2])
		msg.Text = g[3]
	}):
	case matchInto(numberedRe, text, func(g []string) {
		msg.Channel = protocol.ChannelNumbered
		msg.ChannelName = g[1]
		setSender(&msg, g[2])
		msg.Text = g[3]
	}):
	default:
		msg.Channel = protocol.ChannelSystem
		msg.Text = text
	}
	return msg, true
}

func matchInto(re *regexp.Regexp, s string, fill func([]string)) bool {
	g := re.FindStringSubmatch(s)
	if g == nil {
		return false
	}
	fill(g)
	return true
}

func setSender(m *protocol.Message, name string) {
	if i := strings.IndexByte(name, '-'); i >= 0 {
		m.Sender, m.SenderRealm = name[:i], name[i+1:]
		return
	}
	m.Sender = name
}
