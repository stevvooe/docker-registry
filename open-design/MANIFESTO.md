# The "Distribution" project

## What is this

This is a part of the Docker project, or "primitive" that handles the "distribution" of images.

### Punchline

Pack. Sign. Ship. Store. Deliver. Verify.

### Technical scope

Distribution has tight relations with:

 * libtrust, providing cryptographical primitives to handle image signing and verification
 * image format, as transferred over the wire
 * docker-registry, the server side component that allows storage and retrieval of packed images
 * authentication and key management APIs, that are used to verify images and access storage services
 * PKI infrastructure
 * docker core "pull/push client" code gluing all this together - network communication code, tarsum, etc

### Vision

Exchanging images in the docker ecosystem is an important piece of the complete Docker technology stack.

### Mission

Build end-to-end a versatile, reliable and scalable distribution platform that the community can use and depend on, and that ISVs can build on-top of.

### Values

 * "use-case" is first and foremost
 * ship it!
