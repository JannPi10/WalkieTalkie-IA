package models

import (
	"testing"
	"time"
)

func TestChannelMembership_ActivateDeactivate(t *testing.T) {
	membership := ChannelMembership{
		UserID:    1,
		ChannelID: 2,
		Active:    true,
	}

	membership.Deactivate()
	if membership.Active {
		t.Errorf("expected membership to be inactive after Deactivate")
	}
	if membership.LeftAt == nil {
		t.Errorf("expected LeftAt to be set after Deactivate")
	} else if time.Since(*membership.LeftAt) > time.Second {
		t.Errorf("LeftAt timestamp seems incorrect: %v", membership.LeftAt)
	}

	membership.Activate()
	if !membership.Active {
		t.Errorf("expected membership to be active after Activate")
	}
	if membership.LeftAt != nil {
		t.Errorf("expected LeftAt to be nil after Activate")
	}
}
