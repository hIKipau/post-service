package redis

type PostCache struct {
	*Redis
}

func NewPostCache(cache *Redis) *PostCache {
	return &PostCache{Redis: cache}
}
