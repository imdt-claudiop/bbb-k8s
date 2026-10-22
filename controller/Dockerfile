# The cell controller, carrying the chart of the same commit (a cell's version is the chart
# at a commit; the controller refuses another spec.version.chart).
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /controller ./cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=build /controller /controller
COPY chart /chart
ENTRYPOINT ["/controller", "--chart", "/chart"]
