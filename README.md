To set up and run (be sure to have a mongod instance running on localhost on the default port):

    git clone git@github.com:evergreen-ci/logkeeper
    cd logkeeper
    . ./set_gopath.sh
    go run main/logkeeper.go

Example of running resmoke with logkeeper


    # Run this from the root directory where mongodb is cloned to:
    python buildscripts/resmoke.py --suites=core --log=buildlogger  --buildloggerUrl="http://localhost:8080"
