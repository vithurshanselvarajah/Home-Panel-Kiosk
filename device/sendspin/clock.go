package sendspin

import "time"

func nowUs() int64 { return time.Now().UnixMicro() }
