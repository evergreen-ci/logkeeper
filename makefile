# start project configuration
name := logkeeper
buildDir := build
packages := $(name)
orgPath := github.com/evergreen-ci
projectPath := $(orgPath)/$(name)
# end project configuration


# start linting configuration
#   package, testing, and linter dependencies specified
#   separately. This is a temporary solution: eventually we should
#   vendorize all of these dependencies.
lintDeps := github.com/alecthomas/gometalinter
#   include test files and give linters 40s to run to avoid timeouts
lintArgs := --tests --deadline=1m --vendor
#   gotype produces false positives because it reads .a files which
#   are rarely up to date.
lintArgs += --disable="gotype" --disable="gas" --disable="gocyclo"
lintArgs += --disable="aligncheck" --disable="golint" --disable="goconst"
lintArgs += --skip="$(buildDir)" --skip="buildscripts"
#  add and configure additional linters
lintArgs += --enable="goimports" --enable="misspell"
lintArgs += --line-length=100 --dupl-threshold=175
#  two similar functions triggered the duplicate warning, but they're not.
lintArgs += --exclude="file is not goimported" # test files aren't imported
#  golint doesn't handle splitting package comments between multiple files.
lintArgs += --exclude="package comment should be of the form \"Package .* \(golint\)"
#  suppress some lint errors (logging methods could return errors, and error checking in defers.)
lintArgs += --exclude "error return value not checked \(defer.*"
# end lint suppressions

######################################################################
##
## Build, Test, and Dist targets and mechisms.
##
######################################################################

# most of the targets and variables in this section are generic
# instructions for go programs of all kinds, and are not particularly
# specific to evergreen; though the dist targets are more specific than the rest.

# start dependency installation tools
#   implementation details for being able to lazily install dependencies.
#   this block has no project specific configuration but defines
#   variables that project specific information depends on
gopath := $(shell go env GOPATH)
lintDeps := $(addprefix $(gopath)/src/,$(lintDeps))
srcFiles := makefile $(shell find . -name "*.go" -not -path "./$(buildDir)/*" -not -name "*_test.go" -not -path "./buildscripts/*" )
testSrcFiles := makefile $(shell find . -name "*.go" -not -path "./$(buildDir)/*")
testOutput := $(foreach target,$(packages),$(buildDir)/output.$(target).test)
raceOutput := $(foreach target,$(packages),$(buildDir)/output.$(target).race)
testBin := $(foreach target,$(packages),$(buildDir)/test.$(target))
raceBin := $(foreach target,$(packages),$(buildDir)/race.$(target))
coverageOutput := $(foreach target,$(packages),$(buildDir)/output.$(target).coverage)
coverageHtmlOutput := $(foreach target,$(packages),$(buildDir)/output.$(target).coverage.html)
$(gopath)/src/%:
	@-[ ! -d $(gopath) ] && mkdir -p $(gopath) || true
	go get $(subst $(gopath)/src/,,$@)
$(buildDir)/run-linter:buildscripts/run-linter.go $(buildDir)/.lintSetup
	$(vendorGopath) go build -o $@ $<
$(buildDir)/.lintSetup:$(lintDeps)
	@-$(gopath)/bin/gometalinter --install >/dev/null && touch $@
# end dependency installation tools


# distribution targets and implementation
$(buildDir)/build-cross-compile:buildscripts/build-cross-compile.go
	@mkdir -p $(buildDir)
	go build -o $@ $<
$(buildDir)/make-tarball:buildscripts/make-tarball.go $(buildDir)/render-gopath
	$(vendorGopath) go build -o $@ $<
dist:$(buildDir)/dist.tar.gz
dist-test:$(buildDir)/dist-test.tar.gz
dist-race: $(buildDir)/dist-race.tar.gz
dist-source:$(buildDir)/dist-source.tar.gz
distContents := templates public
distContentsArgs := $(foreach dir,$(distContents),--item $(dir))
$(buildDir)/dist.tar.gz:$(buildDir)/make-tarball $(buildDir)/$(name) $(distContents)
	./$< --name $@ --prefix $(name) $(distContentsArgs) --item $(buildDir)/$(name)
$(buildDir)/dist-race.tar.gz:$(buildDir)/make-tarball $(buildDir)/$(name).race $(distContents)
	./$< -name $@ --prefix $(name)-race $(distContentsArgs) --item $(buildDir)/$(name).race
$(buildDir)/dist-test.tar.gz:$(buildDir)/make-tarball makefile $(testBin) $(raceBin)
	./$< -name $@ --prefix $(name)-tests $(foreach item,$(testBin) $(testBin),--item $(item))
$(buildDir)/dist-source.tar.gz:$(buildDir)/make-tarball $(srcFiles) $(testSrcFiles) makefile
	./$< --name $@ --prefix $(name) $(subst $(name),,$(foreach pkg,$(packages),--item ./$(subst -,/,$(pkg)))) --item ./buildscripts --item makefile --exclude "$(name)" --exclude "^.git/" --exclude "$(buildDir)/"
# end main build


# userfacing targets for basic build and development operations
lint:$(buildDir)/output.lint
build:$(buildDir)/$(name)
build-race:$(buildDir)/$(name).race
test:$(foreach target,$(packages),test-$(target))
race:$(foreach target,$(packages),race-$(target))
coverage:$(coverageOutput)
coverage-html:$(coverageHtmlOutput)
list-tests:
	@echo -e "test targets:" $(foreach target,$(packages),\\n\\ttest-$(target))
list-race:
	@echo -e "test (race detector) targets:" $(foreach target,$(packages),\\n\\trace-$(target))
phony := lint build build-race race test coverage coverage-html deps
.PRECIOUS:$(testOutput) $(raceOutput) $(coverageOutput) $(coverageHtmlOutput)
.PRECIOUS:$(foreach target,$(packages),$(buildDir)/test.$(target))
.PRECIOUS:$(foreach target,$(packages),$(buildDir)/race.$(target))
.PRECIOUS:$(foreach target,$(packages),$(buildDir)/output.$(target).lint)
.PRECIOUS:$(buildDir)/output.lint
# end front-ends


# implementation details for building the binary and creating a
# convienent link in the working directory
$(name):$(buildDir)/$(name)
	@[ -e $@ ] || ln -s $<
$(buildDir)/$(name):$(srcFiles)
	$(vendorGopath) go build  -ldflags "-X github.com/evergreen-ci/logkeeper.BuildRevision=`git rev-parse HEAD`" -o $@ main/$(name).go
$(buildDir)/$(name).race:$(srcFiles)
	$(vendorGopath) go build -race  -ldflags "-X github.com/evergreen-ci/logkeeper.BuildRevision=`git rev-parse HEAD`" -o $@ main/$(name).go
phony += $(buildDir)/$(name)
# end main build

# convenience targets for runing tests and coverage tasks on a
# specific package.
race-%:$(buildDir)/output.%.race
	@grep -s -q -e "^PASS" $< && ! grep -s -q "^WARNING: DATA RACE" $<
test-%:$(buildDir)/output.%.test
	@grep -s -q -e "^PASS" $<
coverage-%:$(buildDir)/output.%.coverage
	@grep -s -q -e "^PASS" $<
html-coverage-%:$(buildDir)/output.%.coverage $(buildDir)/output.%.coverage.html
	@grep -s -q -e "^PASS" $<
# end convienence targets


# start vendoring configuration
#    begin with configuration of dependencies
vendorDeps := github.com/Masterminds/glide
vendorDeps := $(addprefix $(gopath)/src/,$(vendorDeps))
vendor-deps:$(vendorDeps)
#   this allows us to store our vendored code in vendor and use
#   symlinks to support vendored code both in the legacy style and with
#   new-style vendor directories. When this codebase can drop support
#   for go1.4, we can delete most of this.
-include $(buildDir)/makefile.vendor
#   nested vendoring is used to support projects that have
nestedVendored := vendor/github.com/mongodb/grip
nestedVendored := $(foreach project,$(nestedVendored),$(project)/build/vendor)
$(buildDir)/makefile.vendor:$(buildDir)/render-gopath makefile
	@mkdir -p $(buildDir)
	@echo "vendorGopath := \$$(shell \$$(buildDir)/render-gopath $(nestedVendored))" >| $@
#   targets for the directory components and manipulating vendored files.
vendor-sync:$(vendorDeps)
	rm -rf vendor
	glide install -s
change-go-version:
	rm -rf $(buildDir)/make-vendor $(buildDir)/render-gopath
	@$(MAKE) $(makeArgs) vendor > /dev/null 2>&1
vendor:$(buildDir)/vendor/src
	@$(MAKE) $(makeArgs) -C vendor/github.com/mongodb/grip $@
vendor-clean:
	rm -rf vendor/github.com/mongodb/grip/vendor/golang.org/x/net
	rm -rf vendor/github.com/mongodb/grip/vendor/github.com/davecgh/go-spew/
	rm -rf vendor/github.com/mongodb/grip/vendor/github.com/stretchr/testify

$(buildDir)/vendor/src:$(buildDir)/make-vendor $(buildDir)/render-gopath
	@./$(buildDir)/make-vendor
#   targets to build the small programs used to support vendoring.
$(buildDir)/make-vendor:buildscripts/make-vendor.go
	@mkdir -p $(buildDir)
	go build -o $@ $<
$(buildDir)/render-gopath:buildscripts/render-gopath.go
	@mkdir -p $(buildDir)
	go build -o $@ $<
#   define dependencies for buildscripts
buildscripts/make-vendor.go:buildscripts/vendoring/vendoring.go
buildscripts/render-gopath.go:buildscripts/vendoring/vendoring.go
#   add phony targets
phony += vendor vendor-deps vendor-clean vendor-sync change-go-version
# end vendoring tooling configuration



# start test and coverage artifacts
#    tests have compile and runtime deps. This varable has everything
#    that the tests actually need to run. (The "build" target is
#    intentional and makes these targets rerun as expected.)
testArgs := -test.v --test.timeout=5m
ifneq (,$(RUN_TEST))
testArgs += -test.run='$(RUN_TEST)'
endif
ifneq (,$(RUN_CASE))
testArgs += -testify.m='$(RUN_CASE)'
endif
#    to avoid vendoring the coverage tool, install it as needed
coverDeps := $(if $(DISABLE_COVERAGE),,golang.org/x/tools/cmd/cover)
coverDeps := $(addprefix $(gopath)/src/,$(coverDeps))
#    implementation for package coverage and test running,mongodb to produce
#    and save test output.
$(buildDir)/test.%:$(testSrcFiles) $(coverDeps)
	$(vendorGopath) go test $(if $(DISABLE_COVERAGE),,-covermode=count) -c -o $@ ./$(subst -,/,$*)
$(buildDir)/race.%:$(testSrcFiles)
	$(vendorGopath) go test -race -c -o $@ ./$(subst -,/,$*)
#  targets to run any tests in the top-level package
$(buildDir)/test.$(name):$(testSrcFiles) $(coverDeps)
	$(vendorGopath) go test $(if $(DISABLE_COVERAGE),,-covermode=count) -c -o $@ ./
$(buildDir)/race.$(name):$(testSrcFiles)
	$(vendorGopath) go test -race -c -o $@ ./
#  targets to run the tests and report the output
$(buildDir)/output.%.test:$(buildDir)/test.% .FORCE
	$(testRunEnv) ./$< $(testArgs) 2>&1 | tee $@
$(buildDir)/output.%.race:$(buildDir)/race.% .FORCE
	$(testRunEnv) ./$< $(testArgs) 2>&1 | tee $@
#  targets to generate gotest output from the linter.
$(buildDir)/output.%.lint:$(buildDir)/run-linter $(testSrcFiles) .FORCE
	@./$< --output=$@ --lintArgs='$(lintArgs)' --packages='$*'
$(buildDir)/output.lint:$(buildDir)/run-linter .FORCE
	@./$< --output="$@" --lintArgs='$(lintArgs)' --packages="$(packages)"
#  targets to process and generate coverage reports
$(buildDir)/output.%.coverage:$(buildDir)/test.% .FORCE $(coverDeps)
	$(testRunEnv) ./$< $(testArgs) -test.coverprofile=$@ | tee $(subst coverage,test,$@)
	@-[ -f $@ ] && go tool cover -func=$@ | sed 's%$(projectPath)/%%' | column -t
$(buildDir)/output.%.coverage.html:$(buildDir)/output.%.coverage $(coverDeps)
	$(vendorGopath) go tool cover -html=$< -o $@
# end test and coverage artifacts


# clean and other utility targets
clean:
	rm -rf $(lintDeps) $(buildDir)/test.* $(buildDir)/coverage.* $(buildDir)/race.* $(projectCleanFiles)
phony += clean
# end dependency targets

# configure phony targets
.FORCE:
.PHONY:$(phony) .FORCE
