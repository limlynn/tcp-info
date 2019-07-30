package collector_test

import (
	"testing"

	"github.com/m-lab/tcp-info/collector"
)

func TestRawReader(t *testing.T) {
	r, err := collector.ReadRawNetlink("testdata/netlinkresult.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 5 {
		t.Fatal("Should have read 5 records")
	}
}
