# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src

# Avval faqat modul fayllari: bog'liqliklar keshi kod o'zgarganda buzilmasin
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/notification .

# distroless/static: shell yo'q, CA sertifikatlari bor, root bo'lmagan foydalanuvchi
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/notification /notification
ENV PORT=8016
EXPOSE 8016
USER nonroot:nonroot
ENTRYPOINT ["/notification"]
