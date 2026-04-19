package runner

import (
	"regexp"
	"sync"
)

var reCacheCI sync.Map // pattern -> *regexp.Regexp

func regexpCompileCI(pattern string) (*regexp.Regexp, error) {
	if v, ok := reCacheCI.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	c, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	reCacheCI.Store(pattern, c)
	return c, nil
}
