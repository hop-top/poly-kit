package stage

import (
	"fmt"
	"time"

	"hop.top/kit/go/runtime/bus"
)

// Topics holds the per-action topic strings the stage primitive emits.
//
// Five events ride here, three for the lifecycle (proposed,
// transitioned, entered), one for time-bounded expiry (expired), one
// for policy denial caused by stage rules (violated — emitted by
// runtime/policy when wired, not by core/stage itself).
//
// Adopters override individual actions with WithTopics or replace all
// five with WithTopicPrefix, mirroring runtime/domain.Topics.
type Topics struct {
	Proposed     bus.Topic
	Transitioned bus.Topic
	Entered      bus.Topic
	Expired      bus.Topic
	Violated     bus.Topic
}

// DefaultTopics is the kit baseline used when no override is supplied.
// Each topic conforms to the kit 4-segment past-tense convention and
// is validated by bus.ValidateTopic in init().
var DefaultTopics = Topics{
	Proposed:     "kit.runtime.stage.proposed",
	Transitioned: "kit.runtime.stage.transitioned",
	Entered:      "kit.runtime.stage.entered",
	Expired:      "kit.runtime.stage.expired",
	Violated:     "kit.runtime.stage.violated",
}

// stageActions is the canonical action list for bus.PrefixTopics.
// Order is fixed so first-failing-action error messages stay
// deterministic.
var stageActions = []string{"proposed", "transitioned", "entered", "expired", "violated"}

// WithTopicPrefix sets all five stage topics from a 3-segment prefix
// of the form "source.category.object". The composed topics are
// "<prefix>.proposed", "<prefix>.transitioned", "<prefix>.entered",
// "<prefix>.expired", "<prefix>.violated".
//
// Example:
//
//	stage.NewManager(stage.WithTopicPrefix("tlc.runtime.stage"))
//
// Panics on validation failure — fail-loud at boot is preferred over
// silent default fallback that would hide subscribers missing events
// at runtime.
func WithTopicPrefix(prefix string) Option {
	tm, err := bus.PrefixTopics(prefix, stageActions)
	if err != nil {
		panic(fmt.Sprintf("stage.WithTopicPrefix(%q): %v", prefix, err))
	}
	t := Topics{
		Proposed:     tm["proposed"],
		Transitioned: tm["transitioned"],
		Entered:      tm["entered"],
		Expired:      tm["expired"],
		Violated:     tm["violated"],
	}
	return func(m *Manager) { m.topics = t }
}

// WithTopics replaces individual action topics. Empty bus.Topic fields
// keep the corresponding DefaultTopics value, so callers can override
// a single action without restating the others.
func WithTopics(t Topics) Option {
	if t.Proposed == "" {
		t.Proposed = DefaultTopics.Proposed
	}
	if t.Transitioned == "" {
		t.Transitioned = DefaultTopics.Transitioned
	}
	if t.Entered == "" {
		t.Entered = DefaultTopics.Entered
	}
	if t.Expired == "" {
		t.Expired = DefaultTopics.Expired
	}
	if t.Violated == "" {
		t.Violated = DefaultTopics.Violated
	}
	return func(m *Manager) { m.topics = t }
}

// init validates DefaultTopics so a typo in a default fails loudly
// when adopters rebuild kit, not at first publish.
func init() {
	for _, t := range []bus.Topic{
		DefaultTopics.Proposed,
		DefaultTopics.Transitioned,
		DefaultTopics.Entered,
		DefaultTopics.Expired,
		DefaultTopics.Violated,
	} {
		if err := bus.ValidateTopic(t); err != nil {
			panic(fmt.Sprintf("stage: malformed default topic %q: %v", t, err))
		}
	}
}

// ProposedPayload is published on Topics.Proposed before persisting a
// transition. Subscribers may veto by returning a non-nil error.
type ProposedPayload struct {
	Scope     string `json:"scope"`
	From      Stage  `json:"from"`
	To        Stage  `json:"to"`
	Principal string `json:"principal,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// TransitionedPayload is published on Topics.Transitioned after a
// successful Set. Post event — subscriber errors are swallowed.
type TransitionedPayload struct {
	Scope     string    `json:"scope"`
	From      Stage     `json:"from"`
	To        Stage     `json:"to"`
	Principal string    `json:"principal,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Since     time.Time `json:"since"`
}

// EnteredPayload is published on Topics.Entered after Topics.Transitioned.
// Subscribers that only care about "what stage am I in now?" listen here
// and ignore from/to.
type EnteredPayload struct {
	Scope     string    `json:"scope"`
	Stage     Stage     `json:"stage"`
	Principal string    `json:"principal,omitempty"`
	Since     time.Time `json:"since"`
}

// ExpiredPayload is published on Topics.Expired by Tick when a State's
// Until has elapsed. Tick does not mutate — subscribers decide whether
// to auto-Set back to StageActive.
type ExpiredPayload struct {
	Scope     string    `json:"scope"`
	Stage     Stage     `json:"stage"`
	ExpiredAt time.Time `json:"expired_at"`
}

// ViolatedPayload is published on Topics.Violated by runtime/policy
// when a stage-driven rule denies an event. The denied event's topic
// is on Topic; the entity kind (track, task, …) is on Entity. Message
// surfaces the policy's user-facing message.
type ViolatedPayload struct {
	Scope     string `json:"scope"`
	Stage     Stage  `json:"stage"`
	Topic     string `json:"topic"`
	Entity    string `json:"entity,omitempty"`
	Principal string `json:"principal,omitempty"`
	Message   string `json:"message,omitempty"`
}
