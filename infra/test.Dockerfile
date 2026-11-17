FROM golang:1.26-alpine
RUN apk add --no-cache git nodejs npm python3 make
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY packages/agents/package*.json packages/agents/
RUN cd packages/agents && npm ci --ignore-scripts
COPY packages packages
COPY apps apps
COPY internal internal
COPY examples examples
COPY schemas schemas
COPY knowledge knowledge
COPY personas personas
COPY scripts scripts
COPY docs docs
COPY Makefile README.md DESIGN.md ./
CMD ["make", "gate"]
