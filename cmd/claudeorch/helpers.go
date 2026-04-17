package main

import "time"

// timeNow returns current UTC time. A var so tests can override it.
var timeNow = func() time.Time { return time.Now().UTC() }
