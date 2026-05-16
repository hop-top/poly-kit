package bus

// TopicFilter controls which topics are forwarded over the network.
// Deny patterns are checked first; if any match, the topic is blocked.
// Allow patterns are checked next; nil allow list means pass all.
type TopicFilter struct {
	Allow []string // glob patterns to allow (nil = allow all)
	Deny  []string // glob patterns to deny (checked first)
}

// Match returns true if topic should be forwarded.
func (f TopicFilter) Match(topic string) bool {
	t := Topic(topic)

	// Deny-first.
	for _, p := range f.Deny {
		if t.Match(p) {
			return false
		}
	}

	// Nil allow = pass all.
	if f.Allow == nil {
		return true
	}

	for _, p := range f.Allow {
		if t.Match(p) {
			return true
		}
	}
	return false
}
