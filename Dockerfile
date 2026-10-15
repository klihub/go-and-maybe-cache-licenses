# go-licenses execs the Go toolchain at runtime - packages.Load runs "go list" -
# so the toolchain has to stay in the final image. An image carrying only the
# binary cannot analyse anything.
#
# The final stage uses the same base as the build stage on purpose. isStdLib
# compares package paths against the GOROOT baked into the binary, so a binary
# built elsewhere stops recognising the standard library and every stdlib
# package is reported as having no module info.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies first, so this layer survives source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/go-and-maybe-cache-licenses .

# Record the licenses of our own dependencies.
RUN go run . save . --save_path /THIRD_PARTY_NOTICES

FROM golang:1.27-alpine

# Not in the base image, and the toolchain needs it to fetch modules straight
# from version control.
RUN apk add --no-cache git

COPY --from=build /out/go-and-maybe-cache-licenses /usr/local/bin/
COPY --from=build /THIRD_PARTY_NOTICES /THIRD_PARTY_NOTICES

# Fail loudly rather than silently downloading a toolchain when a project
# requires a newer Go than this image carries. A silent download would also
# defeat running without network access.
ENV GOTOOLCHAIN=local

# Analysis runs against a mounted source tree.
WORKDIR /work

ENTRYPOINT ["go-and-maybe-cache-licenses"]
