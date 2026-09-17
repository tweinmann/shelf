FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /web .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /web /web
EXPOSE 8080
ENTRYPOINT ["/web"]
