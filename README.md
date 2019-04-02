# prores-go

This is a lightweight decoder written entirely in Go. Furthermore, it has no significant third-party dependencies.

Using the library is as simple as passing in frame bytes to the `DecodeFrame` function:

```go
func DecodeFrame(r io.ReaderAt, size int64) (image.Image, error)
```
