package registry

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker-registry/digest"
	"github.com/docker/docker-registry/storage"
	"github.com/gorilla/handlers"
)

// layerUploadDispatcher constructs and returns the layer upload handler for
// the given request context.
func layerUploadDispatcher(ctx *Context, r *http.Request) http.Handler {
	luh := &layerUploadHandler{
		Context: ctx,
		UUID:    ctx.vars["uuid"],
	}

	handler := http.Handler(handlers.MethodHandler{
		"POST":   http.HandlerFunc(luh.StartLayerUpload),
		"GET":    http.HandlerFunc(luh.GetUploadStatus),
		"HEAD":   http.HandlerFunc(luh.GetUploadStatus),
		"PUT":    http.HandlerFunc(luh.PutLayerChunk),
		"DELETE": http.HandlerFunc(luh.CancelLayerUpload),
	})

	if luh.UUID != "" {
		luh.log = luh.log.WithField("uuid", luh.UUID)

		layers := ctx.services.Layers()
		upload, err := layers.Resume(luh.UUID)

		if err != nil && err != storage.ErrLayerUploadUnknown {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logrus.Infof("error resolving upload: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				luh.Errors.Push(ErrorCodeUnknown, err)
			})
		}

		luh.Upload = upload
		handler = closeResources(handler, luh.Upload)
	}

	return handler
}

// layerUploadHandler handles the http layer upload process.
type layerUploadHandler struct {
	*Context

	// UUID identifies the upload instance for the current request.
	UUID string

	Upload storage.LayerUpload
}

// StartLayerUpload begins the layer upload process and allocates a server-
// side upload session.
func (luh *layerUploadHandler) StartLayerUpload(w http.ResponseWriter, r *http.Request) {
	layers := luh.services.Layers()
	upload, err := layers.Upload(luh.Name)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError) // Error conditions here?
		luh.Errors.Push(ErrorCodeUnknown, err)
		return
	}

	luh.Upload = upload
	defer luh.Upload.Close()

	if err := luh.layerUploadResponse(w, r); err != nil {
		w.WriteHeader(http.StatusInternalServerError) // Error conditions here?
		luh.Errors.Push(ErrorCodeUnknown, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// GetUploadStatus returns the status of a given upload, identified by uuid.
func (luh *layerUploadHandler) GetUploadStatus(w http.ResponseWriter, r *http.Request) {
	if luh.Upload == nil {
		w.WriteHeader(http.StatusNotFound)
		luh.Errors.Push(ErrorCodeUnknownLayerUpload)
	}

	if err := luh.layerUploadResponse(w, r); err != nil {
		w.WriteHeader(http.StatusInternalServerError) // Error conditions here?
		luh.Errors.Push(ErrorCodeUnknown, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PutLayerChunk receives a layer chunk during the layer upload process,
// possible completing the upload with a checksum and length.
func (luh *layerUploadHandler) PutLayerChunk(w http.ResponseWriter, r *http.Request) {
	if luh.Upload == nil {
		w.WriteHeader(http.StatusNotFound)
		luh.Errors.Push(ErrorCodeUnknownLayerUpload)
	}

	var finished bool

	// TODO(stevvooe): This is woefully incomplete. Missing stuff:
	//
	// 1. Extract information from range header, if present.
	// 2. Check offset of current layer.
	// 3. Emit correct error responses.

	// Read in the chunk
	io.Copy(luh.Upload, r.Body)

	if err := luh.maybeCompleteUpload(w, r); err != nil {
		if err != errNotReadyToComplete {
			w.WriteHeader(http.StatusInternalServerError)
			luh.Errors.Push(ErrorCodeUnknown, err)
			return
		}
	}

	if err := luh.layerUploadResponse(w, r); err != nil {
		w.WriteHeader(http.StatusInternalServerError) // Error conditions here?
		luh.Errors.Push(ErrorCodeUnknown, err)
		return
	}

	if finished {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusAccepted)
	}
}

// CancelLayerUpload cancels an in-progress upload of a layer.
func (luh *layerUploadHandler) CancelLayerUpload(w http.ResponseWriter, r *http.Request) {
	if luh.Upload == nil {
		w.WriteHeader(http.StatusNotFound)
		luh.Errors.Push(ErrorCodeUnknownLayerUpload)
	}

}

// layerUploadResponse provides a standard request for uploading layers and
// chunk responses. This sets the correct headers but the response status is
// left to the caller.
func (luh *layerUploadHandler) layerUploadResponse(w http.ResponseWriter, r *http.Request) error {
	uploadURL, err := luh.urlBuilder.forLayerUpload(luh.Upload)
	if err != nil {
		logrus.Infof("error building upload url: %s", err)
		return err
	}

	w.Header().Set("Location", uploadURL)
	w.Header().Set("Content-Length", "0")
	w.Header().Set("Range", fmt.Sprintf("0-%d", luh.Upload.Offset()))

	return nil
}

var errNotReadyToComplete = fmt.Errorf("not ready to complete upload")

// maybeCompleteUpload tries to complete the upload if the correct parameters
// are available. Returns errNotReadyToComplete if not ready to complete.
func (luh *layerUploadHandler) maybeCompleteUpload(w http.ResponseWriter, r *http.Request) error {
	// If we get a digest and length, we can finish the upload.
	dgstStr := r.FormValue("digest") // TODO(stevvooe): Support multiple digest parameters!
	sizeStr := r.FormValue("size")

	if dgstStr == "" || sizeStr == "" {
		return errNotReadyToComplete
	}

	dgst, err := digest.ParseDigest(dgstStr)
	if err != nil {
		return err
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return err
	}

	luh.completeUpload(w, r, size, dgst)
	return nil
}

// completeUpload finishes out the upload with the correct response.
func (luh *layerUploadHandler) completeUpload(w http.ResponseWriter, r *http.Request, size int64, dgst digest.Digest) {
	layer, err := luh.Upload.Finish(size, dgst)
	if err != nil {
		luh.Errors.Push(ErrorCodeUnknown, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	layerURL, err := luh.urlBuilder.forLayer(layer)
	if err != nil {
		luh.Errors.Push(ErrorCodeUnknown, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", layerURL)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusCreated)
}
