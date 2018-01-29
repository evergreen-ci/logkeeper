#!/bin/bash

# this script pulls the evergreen-ci/lobster repo and copies the files so that
# they can be served by logkeeper
# it should only be run from the base logkeeper directory
set -o errexit
set -o xtrace

SCRIPTS_DIR=buildscripts
LOBSTER_REPO=https://github.com/evergreen-ci/lobster.git
LOBSTER_DIR=.lobster-temp
LOBSTER_ASSETS_DIR=build/lobster
LOBSTER_STATIC_DIR=static
LOBSTER_HTML=index.html
LOGKEEPER_STATIC_DIR=public/static
LOGKEEPER_JS_DIR=js
LOGKEEPER_CSS_DIR=css
LOGKEEPER_TEMPLATES_DIR=templates/lobster/build

# clone lobster repo
pushd $SCRIPTS_DIR
rm -rf $LOBSTER_DIR
git clone $LOBSTER_REPO $LOBSTER_DIR
# build lobster
pushd $LOBSTER_DIR
npm install
npm run build
popd && popd

# replace existing js/css/html files in logkeeper with the updated ones
rm -rf $LOGKEEPER_STATIC_DIR/$LOGKEEPER_JS_DIR
rm -rf $LOGKEEPER_STATIC_DIR/$LOGKEEPER_CSS_DIR
cp -R $SCRIPTS_DIR/$LOBSTER_DIR/$LOBSTER_ASSETS_DIR/$LOBSTER_STATIC_DIR/ $LOGKEEPER_STATIC_DIR/
cp $SCRIPTS_DIR/$LOBSTER_DIR/$LOBSTER_ASSETS_DIR/$LOBSTER_HTML $LOGKEEPER_TEMPLATES_DIR/

# surround the html with go template tags
pushd $LOGKEEPER_TEMPLATES_DIR
echo -e "{{define \"base\"}}\n$(cat index.html)" > index.html
echo "{{end}}" >> index.html
popd

# clean up temporary lobster directory
rm -rf $SCRIPTS_DIR/$LOBSTER_DIR/
echo "finished updating lobster"
