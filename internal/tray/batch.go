package tray

import "time"

const (
	// notifyQuiet is how long the pending list must go without a new request
	// before the batch collected so far is announced. It is longer than
	// refreshInterval so a burst that straddles two polls (an app asking for
	// many characters at once) still ends up in a single notification.
	notifyQuiet = 5 * time.Second
	// notifyMaxWait caps how long a batch can be held back by a steady
	// trickle of new requests, so the user is not left uninformed.
	notifyMaxWait = 30 * time.Second
)

// notifyBatcher debounces desktop notifications for pending requests. Each
// poll hands it the IDs currently pending; it returns the IDs to announce
// together in one notification once arrivals have settled, or nil while it
// is still waiting (or when there is nothing new).
type notifyBatcher struct {
	quiet, maxWait time.Duration

	announced map[int64]bool // already included in a notification
	waiting   map[int64]bool // seen, not yet announced
	first     time.Time      // when the current batch started
	last      time.Time      // when the latest arrival in the batch was seen
}

func newNotifyBatcher(quiet, maxWait time.Duration) *notifyBatcher {
	return &notifyBatcher{
		quiet:     quiet,
		maxWait:   maxWait,
		announced: make(map[int64]bool),
		waiting:   make(map[int64]bool),
	}
}

func (b *notifyBatcher) observe(pending []int64, now time.Time) []int64 {
	current := make(map[int64]bool, len(pending))
	arrived := false
	for _, id := range pending {
		current[id] = true
		if !b.announced[id] && !b.waiting[id] {
			b.waiting[id] = true
			arrived = true
		}
	}
	// Forget IDs that are no longer pending (approved/denied), so neither map
	// grows without bound and a request resolved before it was announced
	// isn't announced at all.
	for id := range b.announced {
		if !current[id] {
			delete(b.announced, id)
		}
	}
	for id := range b.waiting {
		if !current[id] {
			delete(b.waiting, id)
		}
	}

	if len(b.waiting) == 0 {
		b.first = time.Time{}
		return nil
	}
	if arrived {
		if b.first.IsZero() {
			b.first = now
		}
		b.last = now
	}
	if now.Sub(b.last) < b.quiet && now.Sub(b.first) < b.maxWait {
		return nil
	}

	out := make([]int64, 0, len(b.waiting))
	for _, id := range pending {
		if b.waiting[id] {
			out = append(out, id)
			b.announced[id] = true
		}
	}
	b.waiting = make(map[int64]bool)
	b.first = time.Time{}
	return out
}
