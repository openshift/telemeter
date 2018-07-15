// Package remoteauthserver implements an HTTP handler that either delegates
// authorization of a token/cluster combo to a remote server via API or performs
// a simple stub authentication.
//
// Remote authorization is performed by:
//
//   1. Encoding the token and cluster into a JSON struct matching TokenRequest
//   2. POSTing that JSON body to the supplied remote endpoint as application/json
//   3. Expecting 200 or 201 as success or a 4xx or 5xx response as error
//   4. Parsing the body of the response as TokenResponse as JSON
//   5. Returning the transformed data from the response to the caller.
//
package remoteauthserver
