# tcp-info
| branch | travis-ci | report-card | coveralls |
|--------|-----------|-----------|-------------|
| master | [![Travis Build Status](https://travis-ci.org/m-lab/tcp-info.svg?branch=master)](https://travis-ci.org/m-lab/tcp-info) | [![Go Report Card](https://goreportcard.com/badge/github.com/m-lab/tcp-info)](https://goreportcard.com/report/github.com/m-lab/tcp-info) | [![Coverage Status](https://coveralls.io/repos/m-lab/tcp-info/badge.svg?branch=master)](https://coveralls.io/github/m-lab/tcp-info?branch=master) |



# Fast tcp-info collector in Go

This repository uses the netlink API to collect inet_diag messages, partially parses them, caches the intermediate representation.
It then detects differences from one scan to the next, and queues connections that have changed for logging.
It logs the intermediate representation through external zstd processes to one file per connection.

The previous version uses protobufs, but we have discontinued that largely because of the increased maintenance overhead, and risk of losing unparsed data.

To run the tests or the collection tool, you will also require zstd, which can be installed with:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/horta/zstd.install/master/install)
```

OR

```bash
sudo apt-get update && sudo apt-get install -y zstd
```


To invoke, with data written to ~/data, and prometheus metrics published on port
7070:
```bash
docker run --network=host -v ~/data:/home/ -it measurementlab/tcp-info -prom=7070
```

# Code Layout

The code needs a bit of restructuring at this point.  Ideally it should look like:

* inetdiag - Should contain ONLY the code related to include/uapi/linux/inet_diag.h
* tcp - Should include ONLY the code related to include/uapi/linux/tcp.h
* netlink - Should include ONLY code related to using the netlink syscall and handling syscall.NetlinkMessage.  It might have a dependency on inetdiag.
* parsing - Should include code related to parsing the messages in inetdiag and tcp.
* zstd - Already fine.  Contains just zstd reader and writer code.
* saver, cache, collector - already fine.

