package main

import (
	"sort"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
)

// fakeKV is an in-memory kvAPI. Atomic KVSetWithOptions with a nil OldValue
// succeeds only when the key is absent — the same compare-and-set the send
// lock relies on.
type fakeKV struct {
	mu    sync.Mutex
	data  map[string][]byte
	lists []kvListCall
	sets  []model.PluginKVSetOptions
	warns []string
}

type kvListCall struct {
	page, count int
}

func newFakeKV() *fakeKV {
	return &fakeKV{data: make(map[string][]byte)}
}

func (f *fakeKV) KVSet(key string, value []byte) *model.AppError {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeKV) KVGet(key string) ([]byte, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), v...), nil
}

func (f *fakeKV) KVDelete(key string) *model.AppError {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeKV) KVList(page, count int) ([]string, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, kvListCall{page: page, count: count})
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	start := page * count
	if start >= len(keys) {
		return []string{}, nil
	}
	end := start + count
	if end > len(keys) {
		end = len(keys)
	}
	return append([]string(nil), keys[start:end]...), nil
}

func (f *fakeKV) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, options)
	if options.Atomic && options.OldValue == nil {
		if _, exists := f.data[key]; exists {
			return false, nil
		}
	}
	f.data[key] = append([]byte(nil), value...)
	return true, nil
}

func (f *fakeKV) LogWarn(msg string, keyValuePairs ...interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.warns = append(f.warns, msg)
}
