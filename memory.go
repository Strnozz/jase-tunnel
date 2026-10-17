package main

import "sync"

// The context budget the computer can hold. The key/value cache grows with the context: a
// 9B model at 32k tokens needs several GB on top of its weights, and on an 8–16 GB laptop
// that means swapping. 16k (Jase's minimum for the tool loop) unless memory is plentiful.
var contextLimit = sync.OnceValue(func() int {
	if total := totalMemory(); total >= 24<<30 {
		return 32768
	}
	return 16384
})
