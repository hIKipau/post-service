package redis

type PostCache struct {
	*Cache
}

func NewPostCache(cache *Cache) *PostCache {
	return &PostCache{Cache: cache}
}
