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
