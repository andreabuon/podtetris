FROM golang:1.26 AS build
WORKDIR /src

# planner/go.mod pins a evictor commit in a branch that may not be published.
# Redirect to the local module for the image build only.
# Build context is src/ (see makefile).
COPY evictor/go.mod evictor/go.sum evictor/
COPY evictor/api/ evictor/api/
COPY planner/go.mod planner/go.sum planner/

WORKDIR /src/planner
RUN go mod edit -replace github.com/andreabuon/podtetris/src/evictor=../evictor
COPY planner/*.go ./
COPY planner/podtetris-scheduler-config.yaml ./
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build -o /out/podtetris-planner

FROM scratch
WORKDIR /
COPY --from=build /out/podtetris-planner /podtetris-planner
COPY --from=build /src/planner/podtetris-scheduler-config.yaml /podtetris-scheduler-config.yaml
ENTRYPOINT ["/podtetris-planner"]
