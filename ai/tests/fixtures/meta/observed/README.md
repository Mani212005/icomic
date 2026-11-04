# Redacted provider-shape replay fixtures

These are shape fixtures, not original prompts or complete original responses. No original image or private story text is copied. Only the HTTP 400 error object is recovered verbatim from a retained body. Transport IDs are synthetic.

| Fixture | Evidence and reconstruction |
| --- | --- |
| http_400 | Exact error object retained in data/icomic-night-d-live-run/live-attempt-2-ai-diagnostics.log; surrounding envelope reconstructed. |
| http_404 | Only HTTP status and model-selection failure survived in live-ai-diagnostics.log and result.md. Message/type reconstructed, not an original body. |
| schema_echo | data/icomic-live-long/ai-job-status.json and live.log retain a truncated Pydantic representation starting with $defs, not the full provider JSON. Schema document and completion envelope reconstructed. |
| empty_length | Existing chat_completion_reasoning_length.json preserves the 17 input/64 completion/62 reasoning diagnostic shape. IDs are synthetic. |
| moderation | Successful completion envelope and 69 input/593 completion usage from live-attempt-3-provider-costs.json; safe text and reasoning detail reconstructed because raw moderation content was not retained. |
| vision | Palette object contract and 3271 input/809 completion usage from attempt 3; palette text and reasoning detail reconstructed. |
| webp_png_label | live-attempt-2-image-shape.json establishes WebP bytes stored with image/png. Response envelope, PNG labels and tiny generated WebP replace unavailable raw response/real artwork. |
| schema_echo, invalid_json, truncated_json | Completion usage 904 input/1995 completion/369 cached input from the failed long run; malformed JSON strings are synthetic negative cases. Truncation uses finish_reason length. |
| characters_cached | Synthetic character text, with the exact cached-token usage shape from the failed long run. |
| palette_schema_echo, palette_invalid_json | Synthetic malformed contents with the observed vision usage; separate from character usage to fit the vision output cap. |
| palette_list, invalid_image, outline_list, characters_object | Synthetic negative shapes exercising strict palette/image validation, not claimed to be observed live. |

Every replay uses an in-memory httpx.MockTransport. Fake adapters can delegate to these replay adapters while retaining their deterministic default behavior. No fixture can send a network request. The matrix tests run real services, queues and cost accounting with these bodies.
