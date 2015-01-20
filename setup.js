db.logs.createIndex({build_id:1, test_id:1, seq:1})
db.logs.createIndex({started:1}, {expireAfterSeconds: 60*60*24*90}) // 90 days retention?
db.tests.createIndex({started:1}, {expireAfterSeconds: 60*60*24*90}) // 90 days retention?
db.builds.createIndex({started:1}, {expireAfterSeconds: 60*60*24*90}) // 90 days retention?
db.tests.createIndex({build_id:1, started:1})
