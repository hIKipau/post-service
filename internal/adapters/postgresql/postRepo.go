package postgresql

type PostRepo struct {
	*PostgreSQL
}

func NewPostRepo(db *PostgreSQL) *PostRepo {
	return &PostRepo{PostgreSQL: db}
}
