FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /server .

FROM alpine:3.20
RUN apk add --no-cache \
    poppler-utils \
    tesseract-ocr \
    tesseract-ocr-data-eng
COPY --from=build /server /server
EXPOSE 3000
CMD ["/server"]
