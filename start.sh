#!/bin/bash 

nohup ./logkeeper --dbhost="logkeeperdb-0.10gen-mci.4085.mongodbdns.com,logkeeperdb-1.10gen-mci.4085.mongodbdns.com" > "~/logkeeperapp.log"
