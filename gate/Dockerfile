FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /jev-gate .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /jev-gate /jev-gate
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/jev-gate"]
