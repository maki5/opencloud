# Webdav

The webdav service, like the [frontend](../frontend) service, provides a HTTP API following the webdav protocol. It receives HTTP calls from requestors like clients and issues gRPC calls to other services executing these requests. After the called service has finished the request, the webdav service will render their responses in `xml` and sends them back to the requestor.

## Endpoints Overview

Currently, the webdav service handles request for two functionalities, which are `Thumbnails` and `Search`.

### Thumbnails

The webdav service provides various `GET` endpoints to get the thumbnails of a file in authenticated and unauthenticated contexts. It also provides thumbnails for spaces on different endpoints. 

Thumbnails for documents, raw photos, audio cover art and other formats the webdav service cannot render itself come from an Apache Tika server (4.1 or newer) when `OC_TIKA_URL` (or `WEBDAV_TIKA_URL`) is set. Every file that is not a directly supported image is sent to Tika; `OC_TIKA_THUMBNAIL_MIME_TYPES` (or `WEBDAV_TIKA_THUMBNAIL_MIME_TYPES`) restricts that to a list of mime types, where an entry like `image/x-raw-samsung:image/x-samsung-srw` maps a type to the one Tika knows the format by. Without Tika, audio cover art and GeoGebra thumbnails are still extracted by the webdav service itself; those built-in extractors are deprecated.

Generated thumbnails are cached by the webdav service itself. The cache backend defaults to `file`, storing entries under `$OC_BASE_DATA_PATH/thumbnails/files` (override with `WEBDAV_THUMBNAIL_CACHE_BACKEND` and `WEBDAV_THUMBNAIL_CACHE_DIR`). Use the `s3` backend when running multiple instances behind a load balancer so they share one cache.

### Search

The webdav service provides access to the search functionality. It offers multiple `REPORT` endpoints for getting search results. 

See the [search](https://github.com/opencloud-eu/opencloud/tree/main/services/search) service for more details about search functionality. 

## Scalability

The webdav service persists generated thumbnails to its thumbnail cache (file backend by default). When running multiple instances behind a load balancer, point `WEBDAV_THUMBNAIL_CACHE_BACKEND` at a shared `s3` bucket so all instances read and write the same cache; otherwise each instance keeps its own on-disk cache.
