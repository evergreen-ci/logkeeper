#!/bin/bash
# this script pulls the evergreen-ci/lobster repo and copies the files so that
# they can be served by logkeeper

cd buildscripts
rm -rf .lobster-temp
git clone https://github.com/evergreen-ci/lobster.git .lobster-temp
cd .lobster-temp
npm install
npm run build
rm -rf ../../public/static/js
rm -rf ../../public/static/css
cp -R build/lobster/static/ ../../public/static/
cp build/lobster/* ../../templates/lobster/build/
cd ../../templates/lobster/build
echo -e "{{define \"base\"}}\n$(cat index.html)" > index.html
echo "{{end}}" >> index.html
rm -rf ../../../buildscripts/.lobster-temp/
