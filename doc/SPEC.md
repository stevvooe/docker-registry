# Docker Registry HTTP API V2

## Introduction

The _Docker Registry HTTP API_ is the protocol to facilitate distribution of
images to the docker engine. It interacts with instances of the docker
registry, which is a service to manage information about docker images and
enable their distribution. The specification covers the operation of version 2
of this API, known as _Docker Registry HTTP API V2_.

While the V2 registry protocol is usable, there are several problems with the
architecture that have led to this new version. The main driver of this
specification these changes to the docker the image format, covered in
docker/docker#8093. The new, self-contained image manifest simplifies image
definition and improves security. This specification will build on that work,
leveraging new properties of the manifest format to improve performance,
reduce bandwidth usage and decrease the likelihood of backend corruption.

For relevant details and history leading up to this specification, please see
the following issues:

- docker/docker#8093
- docker/docker#9015
- docker/docker-registry#612

### Scope

This specification covers the URL layout and protocols of the interaction
between docker registry and docker core. This will affect the docker core
registry API and the rewrite of docker-registry. Docker registry
implementations may implement other API endpoints, but they are not covered by
this specification.

This includes the following features:

- Namespace-oriented URI Layout
- PUSH/PULL registry server for V2 image manifest format
- Resumable layer PUSH support
- V2 Client library implementation

While authentication and authorization support will influence this
specification, details of the protocol will be left to a future specification.
Relevant header definitions and error codes are present to provide an
indication of what a client may encounter.

#### Future

There are features that have been discussed during the process of cutting this
specification. The following is an incomplete list:

- Immutable image references
- Multiple architecture support
- Migration from v2compatibility representation

These may represent features that are either out of the scope of this
specification, the purview of another specification or have been deferred to a
future version.

### Use Cases

For the most part, the use cases of the former registry API apply to the new
version. Differentiating uses cases are covered below.

#### Image Verification

A docker engine instance would like to run verified image named
"library/ubuntu", with the tag "latest". The engine contacts the registry,
requesting the manifest for "library/ubuntu:latest". An untrusted registry
returns a manifest. Before proceeding to download the individual layers, the
engine verifies the manifest's signature, ensuring that the content was
produced from a trusted source and no tampering has occured.

#### Resumable Push

Company X's build servers lose connectivity to docker registry before
completing an image layer transfer. After connectivity returns, the build
server attempts to re-upload the image. The registry notifies the build server
that the upload has already been partially attempted. The build server
responds by only sending the remaining data to complete the image file.

#### Resumable Pull

Company X is having more connectivity problems but this time in their
deployment datacenter. When downloading an image, the connection is
interrupted before completion. The client keeps the partial data and uses http
`Range` requests to avoid downloading repeated data.

#### Layer Upload De-duplication

Company Y's build system creates two identical docker layers from build
processes A and B. Build process A completes uploading the layer before B.
When process B attempts to upload the layer, the registry indicates that its
not necessary because the layer is already known.

If process A and B upload the same layer at the same time, both operations
will proceed and the first to complete will be stored in the registry (Note:
we may modify this to prevent dogpile with some locking mechanism).

### Changes

The V2 specification has been written to work as a living document, specifying
only what is certain and leaving what is not specified open or to future
changes. Only non-conflicting additions should be made to the API and accepted
changes should avoid preventing future changes from happening.

This section should be updated when changes are made to the specification,
indicating what is different. Optionally, we may start marking parts of the specification to correspond with the versions enumerated here.

<dl>
	<dt>2.0</dt>
	<dd>
		This is the baseline specification.
	</dd>
</dl>

## Overview

This section covers client flows and details of the API endpoints. The URI
layout of the new API is structured to support a rich authentication and
authorization model by leveraging namespaces. All endpoints will be prefixed
by the API version and the repository name:

    /v2/<name>/

For example, an API endpoint that will work with the `library/ubuntu`
repository, the URI prefix will be:

    /v2/library/ubuntu/

This scheme provides rich access control over various operations and methods
using the URI prefix and http methods that can be controlled in variety of
ways.

Classically, repository names have always been two path components where each
path component is less than 30 characters. The V2 registry API does not
enforce this. The rules for a repository name are as follows:

1. A repository name is broken up into _path components_. A component of a
   repository name must be at least two characters, optionally separated by
   periods, dashes or underscores. More strictly, it must match the regular
   expression `[a-z0-9]+(?:[._-][a-z0-9]+)*` and the matched result must be 2
   or more characters in length.
2. The name of a repository must have at least two path components, separated
   by a forward slash.
3. The total length of a repository name, including slashes, must be less the
   256 characters.

These name requirements _only_ apply to the registry API and should accept a
superset of what is supported by other docker ecosystem components.

All endpoints should support aggressive http caching, compression and range
headers, where appropriate. The new API attempts to leverage HTTP semantics
where possible but may break from standards to implement targeted features.

For detail on individual endpoints, please see the [_Detail_](#detail)
section.

### Errors

Actionable failure conditions, covered in detail in their relevant sections,
are reported as part of 4xx responses, in a json response body. One or more
errors will be returned in the following format:

    {
        "errors:" [{
                "code": <error identifier>,
                "message": <message describing condition>,
                "detail": <unstructured>
            },
            ...
        ]
    }

The `code` field will be a unique identifier, all caps with underscores by
convention. The `message` field will be a human readable string. The optional
`detail` field may contain arbitrary json data providing information the
client can use to resolve the issue.

While the client can take action on certain error codes, the registry may add
new error codes over time. All client implementations should treat unknown
error codes as `UNKNOWN`, allowing future error codes to be added without
breaking API compatibility. For the purposes of the specification error codes
will only be added and never removed.

For a complete account of all error codes, please see the _Detail_ section.

### API Version Check

A minimal endpoint, mounted at `/v2/` will provide version support information
based on its response statuses. The request format is as follows:

    GET /v2/

If a `200 OK` response is returned, the registry implements the V2(.1)
registry API and the client may proceed safely with other V2 operations.
Optionally, the response may contain information about the supported paths in
the response body. The client should be prepared to ignore this data.

If a `401 Unauthorized` response is returned, the client should take action
based on the contents of the "WWW-Authenticate" header and try the endpoint
again. Depending on access control setup, the client may still have to
authenticate against different resources, even if this check succeeds.

If `404 Not Found` response status, or other unexpected status, is returned,
the client should proceed with the assumption that the registry does not
implement V2 of the API.

### Pulling An Image

An "image" is a combination of a JSON manifest and individual layer files. The
process of pulling an image centers around retrieving these two components.

The first step in pulling an image is to retrieve the manifest. For reference,
the relevant manifest fields for the registry are the following:

 field    | description                                    |
----------|------------------------------------------------|
name      | The name of the image.                         |
tag       | The tag for this version of the image.         |
fsLayers  | A list of layer descriptors (including tarsum) |
signature | A JWS used to verify the manifest content      |

For more information about the manifest format, please see
[docker/docker#8093](https://github.com/docker/docker/issues/8093).

When the manifest is in hand, the client must verify the signature to ensure
the names and layers are valid. Once confirmed, the client will then use the
tarsums to download the individual layers. Layers are stored in as blobs in
the V2 registry API, keyed by their tarsum digest.

#### Pulling an Image Manifest

The image manifest can be fetched with the following url:

```
GET /v2/<name>/manifests/<tag>
```

The "name" and "tag" parameter identify the image and are required.

A `404 Not Found` response will be returned if the image is unknown to the
registry. If the image exists and the response is successful, the image
manifest will be returned, with the following format (see docker/docker#8093
for details):

    {
       "name": <name>,
       "tag": <tag>,
       "fsLayers": [
          {
             "blobSum": <tarsum>
          },
          ...
        ]
       ],
       "history": <v1 images>,
       "signature": <JWS>
    }

The client should verify the returned manifest signature for authenticity
before fetching layers.

#### Pulling a Layer

Layers are stored in the blob portion of the registry, keyed by tarsum digest.
Pulling a layer is carried out by a standard http request. The URL is as
follows:

    GET /v2/<name>/blobs/<tarsum>

Access to a layer will be gated by the `name` of the repository but is
identified uniquely in the registry by `tarsum`. The `tarsum` parameter is an
opaque field, to be interpreted by the tarsum library.

This endpoint may issue a 307 (302 for <HTTP 1.1) redirect to another service
for downloading the layer and clients should be prepared to handle redirects.

This endpoint should support aggressive HTTP caching for image layers. Support
for Etags, modification dates and other cache control headers should be
included. To allow for incremental downloads, `Range` requests should be
supported, as well.

### Pushing An Image

Pushing an image works in the opposite order as a pull. After assembling the
image manifest, the client must first push the individual layers. When the
layers are fully pushed into the registry, the client should upload the signed
manifest.

The details of each step of the process are covered in the following sections.

#### Pushing a Layer

All layer uploads use two steps to manage the upload process. The first step
starts the upload in the registry service, returning a url to carry out the
second step. The second step uses the upload url to transfer the actual data.
Uploads are started with a POST request which returns a url that can be used
to push data and check upload status.

The `Location` header will be used to communicate the upload location after
each request. While it won't change in the this specification, clients should
use the most recent value returned by the API.

##### Starting An Upload

To begin the process, a POST request should be issued in the following format:

```
POST /v2/<name>/blobs/uploads/
```

The parameters of this request are the image namespace under which the layer
will be linked. Responses to this request are covered below.

##### Existing Layers

The existence of a layer can be checked via a `HEAD` request to the blob store
API. The request should be formatted as follows:

```
HEAD /v2/<name>/blobs/<digest>
```

If the layer with the tarsum specified in `digest` is available, a 200 OK
response will be received, with no actual body content (this is according to
http specification). The response will look as follows:

```
200 OK
Content-Length: <length of blob>
```

When this response is received, the client can assume that the layer is
already available in the registry under the given name and should take no
further action to upload the layer. Note that the binary digests may differ
for the existing registry layer, but the tarsums will be guaranteed to match.

##### Uploading the Layer

If the POST request is successful, a `202 Accepted` response will be returned
with the upload URL in the `Location` header:

```
202 Accepted
Location: /v2/<name>/blobs/uploads/<uuid>
Range: bytes=0-<offset>
Content-Length: 0
```

The rest of the upload process can be carried out with the returned url,
called the "Upload URL" from the `Location` header. All responses to the
upload url, whether sending data or getting status, will be in this format.
Though the URI format (`/v2/<name>/blobs/uploads/<uuid>`) for the `Location`
header is specified, clients should treat it as an opaque url and should never
try to assemble the it. While the `uuid` parameter may be an actual UUID, this
proposal imposes no constraints on the format and clients should never impose
any.

##### Upload Progress

The progress and chunk coordination of the upload process will be coordinated
through the `Range` header. While this is a non-standard use of the `Range`
header, there are examples of [similar approaches](https://developers.google.c
om/youtube/v3/guides/using_resumable_upload_protocol) in APIs with heavy use.
For an upload that just started, for an example with a 1000 byte layer file,
the `Range` header would be as follows:

```
Range: bytes=0-0
```

To get the status of an upload, issue a GET request to the upload URL:

```
GET /v2/<name>/blobs/uploads/<uuid>
Host: <registry host>
```

The response will be similar to the above, except will return 204 status:

```
204 No Content
Location: /v2/<name>/blobs/uploads/<uuid>
Range: bytes=0-<offset>
```

Note that the HTTP `Range` header byte ranges are inclusive and that will be
honored, even in non-standard use cases.

##### Monolithic Upload

A monolithic upload is simply a chunked upload with a single chunk and may be
favored by clients that would like to avoided the complexity of chunking. To
carry out a "monolithic" upload, one can simply put the entire content blob to
the provided URL:

```
PUT /v2/<name>/blobs/uploads/<uuid>?digest=<tarsum>[&digest=sha256:<hex digest>]
Content-Length: <size of layer>
Content-Type: application/octet-stream

<Layer Binary Data>
```

The "digest" parameter must be included with the PUT request. Please see the
_Completed Upload_ section for details on the parameters and expected
responses.

Additionally, the download can be completed with a single `POST` request to
the uploads endpoint, including the "size" and "digest" parameters:

```
POST /v2/<name>/blobs/uploads/?digest=<tarsum>[&digest=sha256:<hex digest>]
Content-Length: <size of layer>
Content-Type: application/octet-stream
  
<Layer Binary Data>
```

On the registry service, this should allocate a download, accept and verify
the data and return the same  response as the final chunk of an upload. If the
POST request fails collecting the data in any way, the registry should attempt
to return an error response to the client with the `Location` header providing
a place to continue the download.

The single `POST` method is provided for convenience and most clients should
implement `POST` + `PUT` to support reliable resume of uploads.
  
##### Chunked Upload

To carry out an upload of a chunk, the client can specify a range header and
only include that part of the layer file:

```
PATCH /v2/<name>/blobs/uploads/<uuid>
Content-Length: <size of chunk>
Content-Range: <start of range>-<end of range>
Content-Type: application/octet-stream

<Layer Chunk Binary Data>
```

There is no enforcement on layer chunk splits other than that the server must
receive them in order. The server may enforce a minimum chunk size. If the
server cannot accept the chunk, a `416 Requested Range Not Satisfiable`
response will be returned and will include a `Range` header indicating the
current status:

```
416 Requested Range Not Satisfiable
Location: /v2/<name>/blobs/uploads/<uuid>
Range: 0-<last valid range>
Content-Length: 0
```

If this response is received, the client should resume from the "last valid
range" and upload the subsequent chunk. A 416 will be returned under the
following conditions:

- Invalid Content-Range header format
- Out of order chunk: the range of the next chunk must start immediately after
  the "last valid range" from the previous response.

When a chunk is accepted as part of the upload, a `202 Accepted` response will
be returned, including a `Range` header with the current upload status:

```
202 Accepted
Location: /v2/<name>/blobs/uploads/<uuid>
Range: bytes=0-<offset>
Content-Length: 0
```

##### Completed Upload

For an upload to be considered complete, the client must submit a `PUT`
request on the upload endpoint with a digest parameter. If it is not provided,
the download will not be considered complete. The format for the final chunk
will be as follows:

```
PUT /v2/<name>/blob/uploads/<uuid>?digest=<tarsum>[&digest=sha256:<hex digest>]
Content-Length: <size of chunk>
Content-Range: <start of range>-<end of range>
Content-Type: application/octet-stream

<Last Layer Chunk Binary Data>
```

Optionally, if all chunks have already been uploaded, a `PUT` request with a
`digest` parameter and zero-length body may be sent to complete and validated
the upload. Multiple "digest" parameters may be provided with different
digests. The server may verify none or all of them but _must_ notify the
client if the content is rejected.

When the last chunk is received and the layer has been validated, the client
will receive a `201 Created` response:

```
201 Created
Location: /v2/<name>/blobs/<tarsum>
Content-Length: 0
```

The `Location` header will contain the registry URL to access the accepted
layer file.

###### Digest Parameter

The "digest" parameter is designed as an opaque parameter to support
verification of a successful transfer. The initial version of the registry API
will support a tarsum digest, in the standard tarsum format. For example, a
HTTP URI parameter might be as follows:

```
tarsum.v1+sha256:6c3c624b58dbbcd3c0dd82b4c53f04194d1247c6eebdaab7c610cf7d66709b3b
```

Given this parameter, the registry will verify that the provided content does
result in this tarsum. Optionally, the registry can support other other digest
parameters for non-tarfile content stored as a layer. A regular hash digest
might be specified as follows:

```
sha256:6c3c624b58dbbcd3c0dd82b4c53f04194d1247c6eebdaab7c610cf7d66709b3b
```

Such a parameter would be used to verify that the binary content (as opposed
to the tar content) would be verified at the end of the upload process.

For the initial version, registry servers are only required to support the
tarsum format.

##### Canceling an Upload

An upload can be cancelled by issuing a DELETE request to the upload endpoint.
The format will be as follows:

```
DELETE /v2/<name>/blobs/uploads/<uuid>
```

After this request is issued, the upload uuid will no longer be valid and the
registry server will dump all intermediate data. While uploads will time out
if not completed, clients should issue this request if they encounter a fatal
error but still have the ability to issue an http request.

##### Errors

If an 502, 503 or 504 error is received, the client should assume that the
download can proceed due to a temporary condition, honoring the appropriate
retry mechanism. Other 5xx errors should be treated as terminal.

If there is a problem with the upload, a 4xx error will be returned indicating
the problem. After receiving a 4xx response (except 416, as called out above),
the upload will be considered failed and the client should take appropriate
action.

Note that the upload url will not be available forever. If the upload uuid is
unknown to the registry, a `404 Not Found` response will be returned and the
client must restart the upload process.

#### Pushing an Image Manifest

Once all of the layers for an image are uploaded, the client can upload the
image manifest. An image can be pushed using the following request format:

    PUT /v2/<name>/manifests/<tag>

    {
       "name": <name>,
       "tag": <tag>,
       "fsLayers": [
          {
             "blobSum": <tarsum>
          },
          ...
        ]
       ],
       "history": <v1 images>,
       "signature": <JWS>,
       ...
    }

The `name` and `tag` fields of the response body must match those specified in
the URL.

If there is a problem with pushing the manifest, a relevant 4xx response will
be returned with a JSON error message. Please see the _PUT Manifest section
for details on possible error codes that may be returned.

If one or more layers are unknown to the registry, `BLOB_UNKNOWN` errors are
returned. The `detail` field of the error response will have a `digest` field
identifying the missing blob, which will be a tarsum. An error is returned for
each unknown blob. The response format is as follows:

    {
        "errors:" [{
                "code": "BLOB_UNKNOWN",
                "message": "blob unknown to registry",
                "detail": {
                    "digest": <tarsum>
                }
            },
            ...
        ]
    }

#### Listing Image Tags

It may be necessary to list all of the tags under a given repository. The tags
for an image repository can be retrieved with the following request:

    GET /v2/<name>/tags/list

The response will be in the following format:

    200 OK
    Content-Type: application/json

    {
        "name": <name>,
        "tags": [
            <tag>,
            ...
        ]
    }

For repositories with a large number of tags, this response may be quite
large, so care should be taken by the client when parsing the response to
reduce copying.

### Deleting an Image

An image may be deleted from the registry via its `name` and `tag`. A delete
may be issued with the following request format:

    DELETE /v2/<name>/manifests/<tag>

If the image exists and has been successfully deleted, the following response
will be issued:

    202 Accepted
    Content-Length: None

If the image had already been deleted or did not exist, a `404 Not Found`
response will be issued instead.

## Detail

> **Note**: This section is still under construction. For the purposes of
> implementation, if any details below differ from the described request flows
> above, the section below should be corrected. When they match, this note
> should be removed.

The behavior of the endpoints are covered in detail in this section, organized
by route and entity. All aspects of the request and responses are covered,
including headers, parameters and body formats. Examples of requests and their
corresponding responses, with success and failure, are enumerated.

> **Note**: The sections on endpoint detail are arranged with an example
> request, a description of the request, followed by information about that
> request.

A list of methods and URIs are covered in the table below:

|Method|Path|Entity|Description|
-------|----|------|------------
| GET | `/v2/` | Base | Check that the endpoint implements Docker Registry API V2. |
| GET | `/v2/<name>/tags/list` | Tags | Fetch the tags under the repository identified by `name`. |
| GET | `/v2/<name>/manifests/<tag>` | Manifest | Fetch the manifest identified by `name` and `tag`. |
| PUT | `/v2/<name>/manifests/<tag>` | Manifest | Put the manifest identified by `name` and `tag`. |
| DELETE | `/v2/<name>/manifests/<tag>` | Manifest | Delete the manifest identified by `name` and `tag`. |
| GET | `/v2/<name>/blobs/<digest>` | Blob | Retrieve the blob from the registry identified by `digest`. |
| HEAD | `/v2/<name>/blobs/<digest>` | Blob | Check if the blob is known to the registry. |
| POST | `/v2/<name>/blobs/uploads/` | Intiate Blob Upload | Initiate a resumable blob upload. If successful, an upload location will be provided to complete the upload. Optionally, if the `digest` parameter is present, the request body will be used to complete the upload in a single request. |
| GET | `/v2/<name>/blobs/uploads/<uuid>` | Blob Upload | Retrieve status of upload identified by `uuid`. The primary purpose of this endpoint is to resolve the current status of a resumable upload. |
| HEAD | `/v2/<name>/blobs/uploads/<uuid>` | Blob Upload | Retrieve status of upload identified by `uuid`. This is identical to the GET request. |
| PATCH | `/v2/<name>/blobs/uploads/<uuid>` | Blob Upload | Upload a chunk of data for the specified upload. |
| PUT | `/v2/<name>/blobs/uploads/<uuid>` | Blob Upload | Complete the upload specified by `uuid`, optionally appending the body as the final chunk. |
| DELETE | `/v2/<name>/blobs/uploads/<uuid>` | Blob Upload | Cancel outstanding upload processes, releasing associated resources. If this is not called, the unfinished uploads will eventually timeout. |


The detail for each endpoint is covered in the following sections.

### Errors

The error codes encountered via the API are enumerated in the following table:

|Code|Message|Description|
-------|----|------|------------
 `UNKNOWN` | unknown error | Generic error returned when the error does not have an API classification.
 `DIGEST_INVALID` | provided digest did not match uploaded content | When a blob is uploaded, the registry will check that the content matches the digest provided by the client. The error may include a detail structure with the key "digest", including the invalid digest string. This error may also be returned when a manifest includes an invalid layer digest.
 `SIZE_INVALID` | provided length did not match content length | When a layer is uploaded, the provided size will be checked against the uploaded content. If they do not match, this error will be returned.
 `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned.
 `TAG_INVALID` | manifest tag did not match URI | During a manifest upload, if the tag in the manifest does not match the uri tag, this error will be returned.
 `NAME_UNKNOWN` | repository name not known to registry | This is returned if the name used during an operation is unknown to the registry.
 `MANIFEST_UNKNOWN` | manifest unknown | This error is returned when the manifest, identified by name and tag is unknown to the repository.
 `MANIFEST_INVALID` | manifest invalid | During upload, manifests undergo several checks ensuring validity. If those checks fail, this error may be returned, unless a more specific error is included. The detail will contain information the failed validation.
 `MANIFEST_UNVERIFIED` | manifest failed signature verification | During manifest upload, if the manifest fails signature verification, this error will be returned.
 `BLOB_UNKNOWN` | blob unknown to registry | This error may be returned when a blob is unknown to the registry in a specified repository. This can be returned with a standard get or if a manifest references an unknown layer during upload.
 `BLOB_UPLOAD_UNKNOWN` | blob upload unknown to registry | If a blob upload has been cancelled or was never started, this error code may be returned.



### Base

Base V2 API route. Typically, this can be used for lightweight version checks and to validate registry authorization.



#### GET Base

Check that the endpoint implements Docker Registry API V2.


##### 

```
GET /v2/
Authorization: <scheme> <token>
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Authorization`|header|rfc7235 compliant authorization header.|




###### On Success: OK

```
200 OK
```

The API implements V2 protocol and is accessible.



###### On Failure: Unauthorized

```
401 Unauthorized
WWW-Authenticate: <scheme> realm="<realm>", ..."
```

The client is not authorized to access the registry.

The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`WWW-Authenticate`|An RFC7235 compliant authentication challenge header.|



###### On Failure: Not Found

```
404 Not Found
```

The registry does not implement the V2 API.





### Tags

Retrieve information about tags.



#### GET Tags

Fetch the tags under the repository identified by `name`.


##### 

```
GET /v2/<name>/tags/list
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|




###### On Success: OK

```
200 OK
Content-Type: application/json

{
    "name": <name>,
    "tags": [
        <tag>,
        ...
    ]
}
```

A list of tags for the named repository.



###### On Failure: Not Found

```
404 Not Found
```

The repository is not known to the registry.



###### On Failure: Unauthorized

```
401 Unauthorized
```

The client doesn't have access to repository.





### Manifest

Create, update and retrieve manifests.



#### GET Manifest

Fetch the manifest identified by `name` and `tag`.


##### 

```
GET /v2/<name>/manifests/<tag>
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`tag`|path|Tag of the target manifiest.|




###### On Success: OK

```
200 OK
Content-Type: application/json

{
   "name": <name>,
   "tag": <tag>,
   "fsLayers": [
      {
         "blobSum": <tarsum>
      },
      ...
    ]
   ],
   "history": <v1 images>,
   "signature": <JWS>
}
```

The manifest idenfied by `name` and `tag`.



###### On Failure: Bad Request

```
400 Bad Request
Content-Type: application/json

{
	"errors:" [{
            "code": <error code>,
            "message": "<error message>",
            "detail": ...
        },
        ...
    ]
}
```

The name or tag was invalid.



The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |
| `TAG_INVALID` | manifest tag did not match URI | During a manifest upload, if the tag in the manifest does not match the uri tag, this error will be returned. |



###### On Failure: Not Found

```
404 Not Found
Content-Type: application/json

{
	"errors:" [{
            "code": <error code>,
            "message": "<error message>",
            "detail": ...
        },
        ...
    ]
}
```

The named manifest is not known to the registry.



The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_UNKNOWN` | repository name not known to registry | This is returned if the name used during an operation is unknown to the registry. |
| `MANIFEST_UNKNOWN` | manifest unknown | This error is returned when the manifest, identified by name and tag is unknown to the repository. |




#### PUT Manifest

Put the manifest identified by `name` and `tag`.


##### 

```
PUT /v2/<name>/manifests/<tag>
Authorization: <scheme> <token>
Content-Type: application/json

{
   "name": <name>,
   "tag": <tag>,
   "fsLayers": [
      {
         "blobSum": <tarsum>
      },
      ...
    ]
   ],
   "history": <v1 images>,
   "signature": <JWS>
}
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Authorization`|header|rfc7235 compliant authorization header.|
|`name`|path|Name of the target repository.|
|`tag`|path|Tag of the target manifiest.|




###### On Success: Accepted

```
202 Accepted
```





###### On Failure: Bad Request

```
400 Bad Request
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |
| `TAG_INVALID` | manifest tag did not match URI | During a manifest upload, if the tag in the manifest does not match the uri tag, this error will be returned. |
| `MANIFEST_INVALID` | manifest invalid | During upload, manifests undergo several checks ensuring validity. If those checks fail, this error may be returned, unless a more specific error is included. The detail will contain information the failed validation. |
| `MANIFEST_UNVERIFIED` | manifest failed signature verification | During manifest upload, if the manifest fails signature verification, this error will be returned. |
| `BLOB_UNKNOWN` | blob unknown to registry | This error may be returned when a blob is unknown to the registry in a specified repository. This can be returned with a standard get or if a manifest references an unknown layer during upload. |



###### On Failure: Bad Request

```
400 Bad Request
Content-Type: application/json

{
    "errors:" [{
            "code": "BLOB_UNKNOWN",
            "message": "blob unknown to registry",
            "detail": {
                "digest": <tarsum>
            }
        },
        ...
    ]
}
```

One or more layers may be missing during a manifest upload. If so, the missing layers will be enumerated in the error response.



The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `BLOB_UNKNOWN` | blob unknown to registry | This error may be returned when a blob is unknown to the registry in a specified repository. This can be returned with a standard get or if a manifest references an unknown layer during upload. |



###### On Failure: Unauthorized

```
401 Unauthorized
WWW-Authenticate: <scheme> realm="<realm>", ..."
```



The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`WWW-Authenticate`|An RFC7235 compliant authentication challenge header.|




#### DELETE Manifest

Delete the manifest identified by `name` and `tag`.


##### 

```
DELETE /v2/<name>/manifests/<tag>
Authorization: <scheme> <token>
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Authorization`|header|rfc7235 compliant authorization header.|
|`name`|path|Name of the target repository.|
|`tag`|path|Tag of the target manifiest.|




###### On Success: Accepted

```
202 Accepted
```





###### On Failure: Bad Request

```
400 Bad Request
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |
| `TAG_INVALID` | manifest tag did not match URI | During a manifest upload, if the tag in the manifest does not match the uri tag, this error will be returned. |



###### On Failure: Unauthorized

```
401 Unauthorized
WWW-Authenticate: <scheme> realm="<realm>", ..."
```



The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`WWW-Authenticate`|An RFC7235 compliant authentication challenge header.|



###### On Failure: Not Found

```
404 Not Found
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_UNKNOWN` | repository name not known to registry | This is returned if the name used during an operation is unknown to the registry. |
| `MANIFEST_UNKNOWN` | manifest unknown | This error is returned when the manifest, identified by name and tag is unknown to the repository. |





### Blob

Fetch the blob identified by `name` and `digest`. Used to fetch layers by tarsum digest.



#### GET Blob

Retrieve the blob from the registry identified by `digest`.


##### 

```
GET /v2/<name>/blobs/<digest>
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`digest`|path|Digest of desired blob.|




###### On Success: OK

```
200 OK
Content-Type: application/octet-stream

<blob binary data>
```

The blob identified by `digest` is available. The blob content will be present in the body of the request.
###### On Success: Temporary Redirect

```
307 Temporary Redirect
Location: <blob location>
```

The blob identified by `digest` is available at the provided location.
The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Location`|The location where the layer should be accessible.|




###### On Failure: Bad Request

```
400 Bad Request
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |
| `DIGEST_INVALID` | provided digest did not match uploaded content | When a blob is uploaded, the registry will check that the content matches the digest provided by the client. The error may include a detail structure with the key "digest", including the invalid digest string. This error may also be returned when a manifest includes an invalid layer digest. |



###### On Failure: Unauthorized

```
401 Unauthorized
```





###### On Failure: Not Found

```
404 Not Found
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `NAME_UNKNOWN` | repository name not known to registry | This is returned if the name used during an operation is unknown to the registry. |
| `BLOB_UNKNOWN` | blob unknown to registry | This error may be returned when a blob is unknown to the registry in a specified repository. This can be returned with a standard get or if a manifest references an unknown layer during upload. |




#### HEAD Blob

Check if the blob is known to the registry.


##### 

```
HEAD /v2/<name>/blobs/<digest>
```




The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`digest`|path|Digest of desired blob.|







### Intiate Blob Upload

Initiate a blob upload. This endpoint can be used to create resumable uploads or monolithic uploads.



#### POST Intiate Blob Upload

Initiate a resumable blob upload. If successful, an upload location will be provided to complete the upload. Optionally, if the `digest` parameter is present, the request body will be used to complete the upload in a single request.


##### Initiate Monolithic Blob Upload

```
POST /v2/<name>/blobs/uploads/?digest=<tarsum>
Authorization: <scheme> <token>
Content-Length: <length of blob>
Content-Type: application/octect-stream

<binary data>
```

Upload a blob identified by the `digest` parameter in single request. This upload will not be resumable unless a recoverable error is returned.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Authorization`|header|rfc7235 compliant authorization header.|
|`Content-Length`|header||
|`name`|path|Name of the target repository.|
|`digest`|query|Digest of uploaded blob. If present, the upload will be completed, in a single request, with contents of the request body as the resulting blob.|




###### On Success: Created

```
201 Created
Location: <blob location>
Content-Length: 0
```


The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Location`||
|`Content-Length`|The `Content-Length` header must be zero and the body must be empty.|




###### On Failure: Invalid Name or Digest

```
400 Bad Request
```





The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `DIGEST_INVALID` | provided digest did not match uploaded content | When a blob is uploaded, the registry will check that the content matches the digest provided by the client. The error may include a detail structure with the key "digest", including the invalid digest string. This error may also be returned when a manifest includes an invalid layer digest. |
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |



###### On Failure: Unauthorized

```
401 Unauthorized
WWW-Authenticate: <scheme> realm="<realm>", ..."
```



The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`WWW-Authenticate`|An RFC7235 compliant authentication challenge header.|



The error codes that may be included in the response body are enumerated below:

|Code|Message|Description|
-------|----|------|------------
| `DIGEST_INVALID` | provided digest did not match uploaded content | When a blob is uploaded, the registry will check that the content matches the digest provided by the client. The error may include a detail structure with the key "digest", including the invalid digest string. This error may also be returned when a manifest includes an invalid layer digest. |
| `NAME_INVALID` | manifest name did not match URI | During a manifest upload, if the name in the manifest does not match the uri name, this error will be returned. |



##### Initiate Resumable Blob Upload

```
POST /v2/<name>/blobs/uploads/
Authorization: <scheme> <token>
Content-Length: 0
```

Initiate a resumable blob upload with an empty request body.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Authorization`|header|rfc7235 compliant authorization header.|
|`Content-Length`|header|The `Content-Length` header must be zero and the body must be empty.|
|`name`|path|Name of the target repository.|




###### On Success: Accepted

```
202 Accepted
Content-Length: 0
Location: /v2/<name>/blobs/uploads/<uuid>
Range: 0-0
```

The upload has been created. The `Location` header must be used to complete the upload. The response should identical to a `GET` request on the contents of the returned `Location` header.
The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Content-Length`|The `Content-Length` header must be zero and the body must be empty.|
|`Location`|The location of the created upload. Clients should use the contents verbatim to complete the upload, adding parameters where required.|
|`Range`|Range header indicating the progress of the upload. When starting an upload, it will return an empty range, since no content has been received.|





### Blob Upload

Interact with blob uploads. Clients should never assemble URLs for this endpoint and should only take it through the `Location` header on related API requests.



#### GET Blob Upload

Retrieve status of upload identified by `uuid`. The primary purpose of this endpoint is to resolve the current status of a resumable upload.


##### 

```
GET /v2/<name>/blobs/uploads/<uuid>
```

Retrieve the progress of the current upload, as reported by the `Range` header.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`uuid`|path|A uuid identifying the upload. This field can accept almost anything.|




###### On Success: No Content

```
204 No Content
Range: 0-<offset>
```


The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Range`|Range indicating the current progress of the upload.|




#### HEAD Blob Upload

Retrieve status of upload identified by `uuid`. This is identical to the GET request.


##### 

```
HEAD /v2/<name>/blobs/uploads/<uuid>
```

Retrieve the progress of the current upload, as reported by the `Range` header.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`uuid`|path|A uuid identifying the upload. This field can accept almost anything.|




###### On Success: No Content

```
204 No Content
Range: 0-<offset>
```


The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Range`|Range indicating the current progress of the upload.|




#### PATCH Blob Upload

Upload a chunk of data for the specified upload.


##### 

```
PATCH /v2/<name>/blobs/uploads/<uuid>
Content-Range: <start of range>-<end of range, inclusive>
Content-Length: <length of chunk>
Content-Type: application/octet-stream

<binary chunk>
```

Upload a chunk of data to specified upload without completing the upload.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`Content-Range`|header|Range of bytes identifying the desired block of content represented by the body. Start must the end offset retrieved via status check plus one. Note that this is a non-standard use of the `Content-Range` header.|
|`Content-Length`|header|Length of the chunk being uploaded, corresponding the length of the request body.|
|`name`|path|Name of the target repository.|
|`uuid`|path|A uuid identifying the upload. This field can accept almost anything.|




###### On Success: No Content

```
204 No Content
Range: 0-<offset>
Content-Length: 0
```


The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Range`|Range indicating the current progress of the upload.|
|`Content-Length`|The `Content-Length` header must be zero and the body must be empty.|




#### PUT Blob Upload

Complete the upload specified by `uuid`, optionally appending the body as the final chunk.


##### 

```
PUT /v2/<name>/blobs/uploads/<uuid>?digest=<tarsum>
```

Upload the _final_ chunk of data.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`uuid`|path|A uuid identifying the upload. This field can accept almost anything.|
|`digest`|query|Digest of uploaded blob.|




###### On Success: No Content

```
204 No Content
Content-Range: <start of range>-<end of range, inclusive>
Content-Length: <length of chunk>
Content-Type: application/octet-stream

<binary chunk>
```


The following headers will be returned on the response:

|Name|Description|
|----|-----------|
|`Content-Range`|Range of bytes identifying the desired block of content represented by the body. Start must match the end of offset retrieved via status check. Note that this is a non-standard use of the `Content-Range` header.|
|`Content-Length`|Length of the chunk being uploaded, corresponding the length of the request body.|




#### DELETE Blob Upload

Cancel outstanding upload processes, releasing associated resources. If this is not called, the unfinished uploads will eventually timeout.


##### 

```
DELETE /v2/<name>/blobs/uploads/<uuid>
```

Cancel the upload specified by `uuid`.


The following parameters should be specified on the request:

|Name|Kind|Description|
|----|----|-----------|
|`name`|path|Name of the target repository.|
|`uuid`|path|A uuid identifying the upload. This field can accept almost anything.|







