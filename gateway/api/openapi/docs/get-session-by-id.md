Get a session by id. This endpoint returns a conditional response

- When the query string `extension` is present it will return a payload containing a link to download the session

```json
{
  "download_url": "http://127.0.0.1:8009/api/sessions/<id>/download?token=<token>&extension=csv&newline=1&event-time=0&events=o,e",
  "expire_at": "2024-07-25T15:56:35.317601Z",
}
```

- Fetching the endpoint without any query string returns the payload documented for this endpoint
- The attribute `event_stream` will be rendered differently if the request contains the query string `event_stream=utf8`

```json
{
  (...)
  "event_stream": ["hello world"]
  (...)
}
```

The attribute `metrics` contains the following structure:

```json
{
  "data_masking": {
    "err_count": 0,
    "info_types": {
      "EMAIL_ADDRESS": 1
    },
    "total_redact_count": 1,
    "transformed_bytes": 31
  },
  "event_size": 356
}
```
