package newapi

import (
	"errors"
	"reflect"
	"testing"
)

func TestRestrictToOwnedChannels(t *testing.T) {
	owned := []int{1, 2, 3}

	f := Filter{}
	if err := f.Restrict(owned); err != nil || !reflect.DeepEqual(f.ChannelIDs, owned) {
		t.Fatalf("empty request should mean all owned: %v %v", f.ChannelIDs, err)
	}

	f = Filter{ChannelIDs: []int{2}}
	if err := f.Restrict(owned); err != nil || !reflect.DeepEqual(f.ChannelIDs, []int{2}) {
		t.Fatalf("subset: %v %v", f.ChannelIDs, err)
	}

	f = Filter{ChannelIDs: []int{2, 99}}
	if err := f.Restrict(owned); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("foreign channel: err = %v", err)
	}

	f = Filter{}
	if err := f.Restrict(nil); err != nil || len(f.ChannelIDs) != 0 {
		t.Fatalf("no channels owned: %v %v", f.ChannelIDs, err)
	}
}
