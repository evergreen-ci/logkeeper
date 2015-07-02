#!/bin/bash 

. ./set_gopath.sh
git pull origin master
go build main/logkeeper.go
