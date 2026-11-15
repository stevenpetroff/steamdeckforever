// Package protocol holds the types shared by the chat log reader, the sender
// and the phone-facing server. The wire format is documented in
// docs/protocol.md; keep the two in sync.
package protocol

// Channel is the kind of chat line, mapped from WoW's chat log prefixes.
type Channel string

const (
	ChannelSay         Channel = "say"
	ChannelYell        Channel = "yell"
	ChannelWhisper     Channel = "whisper"
	ChannelParty       Channel = "party"
	ChannelPartyLeader Channel = "party_leader"
	ChannelRaid        Channel = "raid"
	ChannelRaidLeader  Channel = "raid_leader"
	ChannelRaidWarning Channel = "raid_warning"
	ChannelGuild       Channel = "guild"
	ChannelOfficer     Channel = "officer"
	ChannelNumbered    Channel = "channel" // General, Trade, LocalDefense, LFG, ...
	ChannelEmote       Channel = "emote"
	ChannelSystem      Channel = "system"
)

// Message is one parsed chat log line.
type Message struct {
	ID          string  `json:"id"`   // assigned by the hub, monotonically increasing
	Time        string  `json:"time"` // as written in the log, e.g. "11/14 18:02:11.204"
	Channel     Channel `json:"channel"`
	ChannelName string  `json:"channel_name,omitempty"` // e.g. "2. Trade - City" for numbered channels
	Sender      string  `json:"sender,omitempty"`
	SenderRealm string  `json:"sender_realm,omitempty"`
	Text        string  `json:"text"`
	Outgoing    bool    `json:"outgoing,omitempty"` // true for "To [Name]: ..." whispers
}

// Outcome of a send request.
type Outcome string

const (
	OutcomeSent    Outcome = "sent"
	OutcomeNotSent Outcome = "not_sent"
)

// Reason explains a not_sent outcome. The set is closed (plan KTD7).
type Reason string

const (
	ReasonNotFocused       Reason = "not_focused"
	ReasonFocusLost        Reason = "focus_lost"
	ReasonTooLong          Reason = "too_long"
	ReasonInvalidText      Reason = "invalid_text"
	ReasonInputUnavailable Reason = "input_unavailable"
	ReasonStale            Reason = "stale"
)

// MaxMessageBytes is WoW's chat edit box limit in UTF-8 bytes.
const MaxMessageBytes = 255

// MaxPasteBytes caps paste mode (WeakAura strings and the like). It keeps
// the clipboard property inside one X request (about 256 KiB without the
// BIG-REQUESTS extension).
const MaxPasteBytes = 192 << 10

// Mode is how a phone message is delivered.
type Mode int

const (
	// ModeChat opens a clean chat box, pastes one line and presses Enter.
	ModeChat Mode = iota
	// ModePaste pastes into whichever WoW text box has focus, without Enter.
	ModePaste
)

// SendResult is the final result of one send request.
type SendResult struct {
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
	Reason  Reason  `json:"reason,omitempty"`
}

// Status is the service health shown on the phone status line.
type Status struct {
	WoWFocused     bool   `json:"wow_focused"`
	LogFresh       bool   `json:"log_fresh"`
	LastLogAt      string `json:"last_log_at,omitempty"` // RFC 3339
	InputAvailable bool   `json:"input_available"`
	InstancePaused bool   `json:"instance_paused"`
}
