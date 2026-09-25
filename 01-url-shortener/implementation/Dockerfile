FROM golang:1.27

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o server ./cmd/server

EXPOSE 8080

CMD ["./server"]