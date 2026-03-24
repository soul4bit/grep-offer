package main

import (
  "context"
  "fmt"
  "io"
  "net/http"
  "net/http/httptest"
  "time"
  apppkg "grep-offer/internal/app"
)

func main() { _ = context.Background(); _ = fmt.Println; _ = io.ReadAll; _ = httptest.NewServer; _ = time.Now; _ = http.MethodGet; _ = apppkg.Config{} }
