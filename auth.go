package registry

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Sirupsen/logrus"
)

// Operation represents an action on the registry.
type Operation int

const (
	OperationPull Operation = 1 << iota
	OperationPush

	OperationNone = 0
	OperationAll  = OperationPull | OperationPush
)

func (op Operation) String() string {
	var opstrings []string
	if op&OperationPush > 0 {
		opstrings = append(opstrings, "push")
	}

	if op&OperationPull > 0 {
		opstrings = append(opstrings, "pull")
	}

	return strings.Join(opstrings, ",")
}

// Access describes the operations on a target resource.
type Access struct {
	Repository string
	Operations Operation
}

// AccessController controls access to registry resources access based on
// operation. Implementations can both support complete denial, along with
// http challenge authentication.
type AccessController interface {
	// Authorized returns non-nil if the request is granted the request
	// access. If the error is non-nil, access should always be denied. If the
	// error is of type Challenge, a 401 authenticate response should be
	// issued with the contents of the challenge.
	//
	// In the future, other error types, besides Challenge, may be added to
	// support more complex authentication flows.
	Authorized(req *http.Request, access Access) error
}

// Challenge represents the contents of the WWW-Authenticate header for an
// authentication challenge. Returned as an error by the access controller,
// the string value of an error can be used as the valid header value, and
// should comply with http://tools.ietf.org/html/rfc7235.
type Challenge struct {
	// Scheme is a valid authentication scheme, as registered at
	// http://www.iana.org/assignments/http-authschemes/http-
	// authschemes.xhtml. There is no validation that it belongs to the list
	// of registered schemes. The value will be titlecased when placed in the
	// header.
	Scheme string

	// Parameters will become the parameters for the authentication challenge.
	// For example, a basic auth challenge would have "realm" as one of the
	// parameters.
	Parameters map[string]string
}

// Error returns an error string, which is a valid WWW-Authenticate header.
func (ch Challenge) Error() string {

	// TODO(stevvooe): We need to take the time to get the escaping right for
	// these header values.

	var pairs []string
	for k, v := range ch.Parameters {
		pairs = append(pairs, k+"="+strconv.Quote(v))
	}

	return fmt.Sprintf("%s %s", strings.Title(ch.Scheme), strings.Join(pairs, ", "))
}

// TODO(stevvooe): Replace the silly access controller with something less
// silly.

// sillyAccessController checks that there is an Authorization header
type sillyAccessController struct {
	realm   string
	service string
}

func (sac sillyAccessController) Authorized(req *http.Request, access Access) error {
	if req.Header.Get("Authorization") == "" || access.Operations == OperationNone {
		parameters := map[string]string{
			"realm":   sac.realm,
			"service": sac.service,
		}

		logrus.Infof("operations: %v", access.Operations)
		var scopes []string
		resource := fmt.Sprintf("repository:%s", access.Repository)

		if access.Operations&OperationPull != 0 {
			scopes = append(scopes, resource+":pull")
		}

		if access.Operations&OperationPush != 0 {
			scopes = append(scopes, resource+":push")
		}

		logrus.Infof("scopes: %v", scopes)
		if len(scopes) > 0 {
			parameters["scope"] = strings.Join(scopes, " ")
		}
		return Challenge{
			Scheme:     "Bearer",
			Parameters: parameters,
		}
	}

	return nil
}
