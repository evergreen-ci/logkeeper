package db

import (
	"github.com/evergreen-ci/logkeeper/env"
	"go.mongodb.org/mongo-driver/mongo"
)

func DB() *mongo.Database {
	return env.Client().Database(env.DBName())
}

func C(collectionName string) *mongo.Collection {
	return env.Client().Database(env.DBName()).Collection(collectionName)
}
