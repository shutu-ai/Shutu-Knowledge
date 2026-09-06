package parser

import "time"

func timeDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
