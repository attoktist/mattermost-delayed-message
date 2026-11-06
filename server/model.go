package main

const (
	StatusPending   = "pending"
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusCompleted = "completed"

	// TriggerOnline posts a direct message once PeerID is online. SendAt stays 0.
	TriggerOnline = "online"

	RepeatNone        = ""
	RepeatDaily       = "daily"
	RepeatWeekdays    = "weekdays"
	RepeatWeekly      = "weekly"
	RepeatFortnightly = "fortnightly"
	RepeatMonthly     = "monthly"
	RepeatYearly      = "yearly"

	EndsNever = "never"
	EndsOn    = "on"
	EndsAfter = "after"
)

type ScheduledMessage struct {
	ID         string   `json:"id"`
	ChannelIDs []string `json:"channel_ids,omitempty"`
	// ChannelID is the legacy single-channel field. It is only read on
	// unmarshal of pre-multi-channel records; new records write ChannelIDs.
	// normalizeTargets() promotes it into ChannelIDs and clears it on load.
	ChannelID string `json:"channel_id,omitempty"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	SendAt    int64  `json:"send_at"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`

	// Recurrence (all omitempty so existing rows deserialise as zero-values =
	// one-off, identical to the pre-recurrence behaviour).
	Repeat      string `json:"repeat,omitempty"`
	Timezone    string `json:"tz,omitempty"`
	EndsMode    string `json:"ends_mode,omitempty"`
	EndsAt      int64  `json:"ends_at,omitempty"`
	EndsAfter   int    `json:"ends_after,omitempty"`
	Occurrences int    `json:"occurrences,omitempty"`

	// Random fire window: when WindowMs > 0 the actual fire instant is FireAt,
	// chosen randomly in [SendAt, SendAt+WindowMs). SendAt stays as the
	// window-start anchor so nextOccurrence re-anchors recurring schedules.
	WindowMs int64 `json:"window_ms,omitempty"`
	FireAt   int64 `json:"fire_at,omitempty"`

	// Multi-message rotation (recurring only). Messages is the rotation pool;
	// MessageCycle is the current shuffled order of indices into Messages,
	// MessageCyclePos is the next index within the cycle to send.
	// LastSentIndex remembers the previous occurrence's index so a new cycle
	// can avoid starting with the message that just fired.
	Messages        []string `json:"messages,omitempty"`
	MessageCycle    []int    `json:"message_cycle,omitempty"`
	MessageCyclePos int      `json:"message_cycle_pos,omitempty"`
	LastSentIndex   *int     `json:"last_sent_index,omitempty"`

	// Empty Trigger is a timed message. TriggerOnline waits on PeerID.
	Trigger string `json:"trigger,omitempty"`
	PeerID  string `json:"peer_id,omitempty"`
}

// Targets returns the channel IDs this message will post to. Always read
// targets through this helper rather than touching ChannelIDs/ChannelID
// directly — it isolates callers from the legacy single-channel shape.
func (m *ScheduledMessage) Targets() []string {
	if len(m.ChannelIDs) > 0 {
		return m.ChannelIDs
	}
	if m.ChannelID != "" {
		return []string{m.ChannelID}
	}
	return nil
}

// normalizeTargets promotes a legacy ChannelID into ChannelIDs so downstream
// code sees a single shape. Called by load paths after json.Unmarshal.
func (m *ScheduledMessage) normalizeTargets() {
	if len(m.ChannelIDs) == 0 && m.ChannelID != "" {
		m.ChannelIDs = []string{m.ChannelID}
	}
	m.ChannelID = ""
}

// effectiveFireAt is the moment the scheduler should actually post. For
// windowed schedules this is the per-occurrence randomised FireAt; otherwise
// (and for legacy records) it falls back to SendAt.
func (m *ScheduledMessage) effectiveFireAt() int64 {
	if m.FireAt > 0 {
		return m.FireAt
	}
	return m.SendAt
}
