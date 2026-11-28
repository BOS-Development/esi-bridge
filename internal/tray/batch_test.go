package tray

import (
	"reflect"
	"testing"
	"time"

	"github.com/BOS-Development/esi-bridge/internal/store"
)

func TestNotifyBatcherCoalescesBurst(t *testing.T) {
	b := newNotifyBatcher(5*time.Second, 30*time.Second)
	t0 := time.Unix(1000, 0)

	// A burst straddling two polls.
	if got := b.observe([]int64{1, 2}, t0); got != nil {
		t.Fatalf("first poll announced %v, want nothing yet", got)
	}
	if got := b.observe([]int64{1, 2, 3, 4}, t0.Add(3*time.Second)); got != nil {
		t.Fatalf("second poll announced %v, want nothing yet", got)
	}
	// Quiet for 3s: still under the 5s settle period.
	if got := b.observe([]int64{1, 2, 3, 4}, t0.Add(6*time.Second)); got != nil {
		t.Fatalf("announced %v before burst settled", got)
	}
	got := b.observe([]int64{1, 2, 3, 4}, t0.Add(9*time.Second))
	if want := []int64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("announced %v, want %v", got, want)
	}
	// Already announced: nothing more.
	if got := b.observe([]int64{1, 2, 3, 4}, t0.Add(30*time.Second)); got != nil {
		t.Fatalf("re-announced %v", got)
	}
}

func TestNotifyBatcherMaxWait(t *testing.T) {
	b := newNotifyBatcher(5*time.Second, 30*time.Second)
	t0 := time.Unix(1000, 0)
	var ids []int64
	var got []int64
	for i := 0; i <= 10 && got == nil; i++ {
		ids = append(ids, int64(i))
		got = b.observe(ids, t0.Add(time.Duration(i)*3*time.Second))
	}
	if len(got) != 11 {
		t.Fatalf("trickle announced %v, want all 11 at the 30s cap", got)
	}
}

func TestNotifyBatcherDropsResolved(t *testing.T) {
	b := newNotifyBatcher(5*time.Second, 30*time.Second)
	t0 := time.Unix(1000, 0)
	b.observe([]int64{1, 2}, t0)
	// 1 approved before the batch settled: only 2 is announced.
	got := b.observe([]int64{2}, t0.Add(6*time.Second))
	if want := []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("announced %v, want %v", got, want)
	}
	// Everything resolved, then a fresh request starts a new batch.
	if got := b.observe(nil, t0.Add(9*time.Second)); got != nil {
		t.Fatalf("announced %v with nothing pending", got)
	}
	if got := b.observe([]int64{5}, t0.Add(12*time.Second)); got != nil {
		t.Fatalf("announced %v immediately", got)
	}
	got = b.observe([]int64{5}, t0.Add(18*time.Second))
	if want := []int64{5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("announced %v, want %v", got, want)
	}
}

func TestDescribeGrants(t *testing.T) {
	one := []store.PendingGrant{{ApplicationName: "App", CharacterName: "Alice"}}
	if got, want := describeGrants(one), "App wants access to Alice"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	same := []store.PendingGrant{
		{ApplicationName: "App", CharacterName: "Alice"},
		{ApplicationName: "App", CharacterName: "Bob"},
		{ApplicationName: "App", CharacterName: "Carol"},
	}
	if got, want := describeGrants(same), "App wants access to 3 characters"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	mixed := []store.PendingGrant{
		{ApplicationName: "App", CharacterName: "Alice"},
		{ApplicationName: "Other", CharacterName: "Bob"},
	}
	if got, want := describeGrants(mixed), "2 new access requests are waiting"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribePermissions(t *testing.T) {
	same := []store.PendingAppPermission{
		{ApplicationName: "App", Permission: "a"},
		{ApplicationName: "App", Permission: "b"},
	}
	if got, want := describePermissions(same), "App is requesting 2 permissions"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	mixed := []store.PendingAppPermission{
		{ApplicationName: "App", Permission: "a"},
		{ApplicationName: "Other", Permission: "b"},
	}
	if got, want := describePermissions(mixed), "2 new permission requests are waiting"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
