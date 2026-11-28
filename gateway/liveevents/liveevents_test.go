package liveevents

import "testing"

func TestASubscriberGetsItsOrganizationsEvents(t *testing.T) {
	mine, stopMine := Subscribe("org-a")
	defer stopMine()
	other, stopOther := Subscribe("org-b")
	defer stopOther()

	Publish("org-a", Event{Type: SidecarCheckedIn, ID: "sc-1"})

	if got := <-mine; got.Type != SidecarCheckedIn || got.ID != "sc-1" {
		t.Fatalf("event = %+v", got)
	}
	select {
	case ev := <-other:
		t.Fatalf("another organization got %+v", ev)
	default:
	}
}

// A reader that stops taking events never blocks the publisher, which is a
// sidecar handshake.
func TestAFullSubscriberDoesNotBlockPublish(t *testing.T) {
	_, stop := Subscribe("org-full")
	defer stop()
	for i := 0; i < subscriberBuffer*3; i++ {
		Publish("org-full", Event{Type: SidecarCheckedIn, ID: "sc"})
	}
}

func TestStopEndsTheSubscription(t *testing.T) {
	ch, stop := Subscribe("org-stop")
	stop()
	stop() // twice is harmless
	if _, open := <-ch; open {
		t.Fatal("the channel is still open after stop")
	}
	Publish("org-stop", Event{Type: SidecarCheckedIn}) // no subscriber left, no panic
}

func TestAudienceAllowsTheGroupMembersButNotTheOwner(t *testing.T) {
	a := &Audience{Groups: []string{"dba", "sre"}, Emails: []string{"ana@example.com"}, OwnerID: "owner"}
	for _, tc := range []struct {
		name    string
		user    string
		email   string
		groups  []string
		isAdmin bool
		want    bool
	}{
		{"a reviewer", "u1", "u1@example.com", []string{"eng", "dba"}, false, true},
		{"a force approver", "u2", "u2@example.com", []string{"sre"}, false, true},
		{"a named email", "u5", "Ana@Example.com", []string{"eng"}, false, true},
		{"outside the groups", "u3", "u3@example.com", []string{"eng"}, false, false},
		{"an admin outside the groups", "u4", "u4@example.com", []string{"admin"}, true, false},
		{"the owner in a group", "owner", "owner@example.com", []string{"dba"}, false, false},
		{"the owner as an admin in a group", "owner", "owner@example.com", []string{"dba", "admin"}, true, true},
	} {
		if got := a.Allows(tc.user, tc.email, tc.groups, tc.isAdmin); got != tc.want {
			t.Errorf("%s: Allows = %v, want %v", tc.name, got, tc.want)
		}
	}
	if (*Audience)(nil).Allows("u1", "ana@example.com", []string{"dba"}, true) {
		t.Error("an event with no audience reached a user")
	}
}
