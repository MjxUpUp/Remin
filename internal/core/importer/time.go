package importer

import "time"

var unixTime = func(sec int64) time.Time { return time.Unix(sec, 0) }
