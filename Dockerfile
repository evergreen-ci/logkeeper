# Install base dependencies
FROM buildpack-deps:wheezy-scm
RUN apt-get update && apt-get install -y \
                gcc libc6-dev make \
                --no-install-recommends \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get clean
ENV GOLANG_VERSION 1.4.2
RUN curl -sSL https://golang.org/dl/go$GOLANG_VERSION.src.tar.gz \
    | tar -v -C /usr/src -xz
RUN cd /usr/src/go/src && ./make.bash --no-clean 2>&1
ENV PATH /usr/src/go/bin:$PATH

# Create and migrate application enviornment
RUN mkdir -p /app /app/.gopath/src/github.com/evergreen-ci
WORKDIR /app
ADD . /app

# Set up Go environment.
ENV GOPATH /app/.gopath:/app/vendor
RUN ln -s /app /app/.gopath/src/github.com/evergreen-ci/logkeeper

# build application
RUN go build main/logkeeper.go

# Start application
EXPOSE 3000
# ENV MONGODB_URI
CMD ["./logkeeper", "--port", "3000"]
