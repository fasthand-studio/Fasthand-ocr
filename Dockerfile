# Build the Go OCR service into a small static image.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fasthand-ocr ./cmd/fasthand-ocr

FROM gcr.io/distroless/static-debian12:nonroot
ENV ADDR=:8080
COPY --from=build /out/fasthand-ocr /usr/local/bin/fasthand-ocr
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fasthand-ocr"]
