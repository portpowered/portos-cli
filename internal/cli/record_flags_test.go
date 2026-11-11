package cli

import "testing"

func TestRecordAndSnapshotParseRelayOnly(t *testing.T) {
	record := (&RecordCommand{}).Generate()
	if err := record.ParseFlags([]string{"--relay-only", "-o", "clip.mp4", "-d", "30"}); err != nil {
		t.Fatal(err)
	}
	if relayOnly, err := record.Flags().GetBool("relay-only"); err != nil || !relayOnly {
		t.Fatalf("record --relay-only parsed as %v (%v)", relayOnly, err)
	}
	snapshot := (&SnapshotCommand{}).Generate()
	if relayOnly, err := snapshot.Flags().GetBool("relay-only"); err != nil || relayOnly {
		t.Fatalf("snapshot --relay-only defaults to %v (%v), want false", relayOnly, err)
	}
	if err := snapshot.ParseFlags([]string{"--relay-only"}); err != nil {
		t.Fatal(err)
	}
	if relayOnly, _ := snapshot.Flags().GetBool("relay-only"); !relayOnly {
		t.Fatal("snapshot --relay-only did not parse")
	}
}
