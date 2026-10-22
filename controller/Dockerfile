# The cell controller, carrying the chart of the same commit (a cell's version is the chart
# at a commit; the controller refuses another spec.version.chart).
# Build context is the REPO ROOT (docker build -f controller/Dockerfile .): the chart lives
# at the root, the Go module under controller/.
FROM golang:1.24 AS build
WORKDIR /src
COPY controller/go.mod controller/go.sum ./
RUN go mod download
COPY controller/ .
RUN CGO_ENABLED=0 go build -o /controller ./cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=build /controller /controller
COPY chart /chart
ENTRYPOINT ["/controller", "--chart", "/chart"]
