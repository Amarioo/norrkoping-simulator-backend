# The simulator as one static binary; the map data is embedded in it.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd cmd
COPY internal internal
COPY src src
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /simulator ./cmd/simulator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /simulator /simulator
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
ENTRYPOINT ["/simulator"]
