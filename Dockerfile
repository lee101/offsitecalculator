FROM golang:1.23 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /offsite ./cmd/offsite

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /offsite /offsite
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/offsite"]
