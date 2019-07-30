package collector

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/m-lab/tcp-info/netlink"
)

// NetlinkResult is used for storing groups of raw netlink messages for test.
type NetlinkResult struct {
	Time time.Time
	IPv6 []*netlink.NetlinkMessage
	IPv4 []*netlink.NetlinkMessage
}

// ReadRawNetlink reads the entire contents of a NetlinkResult jsonl file.
// Used only for testing.
func ReadRawNetlink(filename string) ([]NetlinkResult, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rdr := bufio.NewReader(f)

	result := make([]NetlinkResult, 0, 100)

	for {
		l, err := rdr.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return result, err
			}
			break
		}
		nr := NetlinkResult{}
		err = json.Unmarshal([]byte(l), &nr)
		if err != nil {
			return result, err
		}
		result = append(result, nr)
	}

	return result, nil
}
