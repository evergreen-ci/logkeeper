To set up and run (be sure to have a mongod instance running on localhost on the default port):

```sh
    git clone git@github.com:evergreen-ci/logkeeper
    cd logkeeper
    
    # Use this (or similar command) to seed some sample data into a local bucket and use that as storage
    mkdir -p _bucketdata && cp -r testdata/simple/* _bucketdata
    go run main/logkeeper.go --localPath _bucketdata
```

Example of running resmoke with logkeeper


    # Run this from the root directory where mongodb is cloned to:
    python buildscripts/resmoke.py --suites=core --log=buildlogger  --buildloggerUrl="http://localhost:8080"

