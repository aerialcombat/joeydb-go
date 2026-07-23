package joeydb

import (
	"context"
	"fmt"

	querypkg "github.com/aerialcombat/joeydb-go/query"
)

// QueryTable validates that request selects a table result, then executes and
// decodes it without requiring a caller-owned response struct.
func (c *Client) QueryTable(
	ctx context.Context,
	request querypkg.Request,
	options ...RequestOption,
) (querypkg.TableResult, *Response, error) {
	var result querypkg.TableResult
	if err := validateTypedQueryShape(request, querypkg.ShapeTable); err != nil {
		return result, nil, err
	}
	response, err := c.QueryRequest(ctx, request, &result, options...)
	if err != nil {
		return result, response, err
	}
	if err := validateTypedQueryResult(
		response, result.Shape, querypkg.ShapeTable, result.Table != nil,
	); err != nil {
		return result, response, err
	}
	return result, response, nil
}

// QueryGraph validates that request selects a graph result, then executes and
// decodes it without requiring a caller-owned response struct.
func (c *Client) QueryGraph(
	ctx context.Context,
	request querypkg.Request,
	options ...RequestOption,
) (querypkg.GraphResult, *Response, error) {
	var result querypkg.GraphResult
	if err := validateTypedQueryShape(request, querypkg.ShapeGraph); err != nil {
		return result, nil, err
	}
	response, err := c.QueryRequest(ctx, request, &result, options...)
	if err != nil {
		return result, response, err
	}
	if err := validateTypedQueryResult(
		response, result.Shape, querypkg.ShapeGraph, result.Graph != nil,
	); err != nil {
		return result, response, err
	}
	return result, response, nil
}

// QueryDocument validates that request selects a document result, then
// executes and decodes it without requiring a caller-owned response struct.
func (c *Client) QueryDocument(
	ctx context.Context,
	request querypkg.Request,
	options ...RequestOption,
) (querypkg.DocumentResult, *Response, error) {
	var result querypkg.DocumentResult
	if err := validateTypedQueryShape(request, querypkg.ShapeDocument); err != nil {
		return result, nil, err
	}
	response, err := c.QueryRequest(ctx, request, &result, options...)
	if err != nil {
		return result, response, err
	}
	if err := validateTypedQueryResult(
		response, result.Shape, querypkg.ShapeDocument, result.Document != nil,
	); err != nil {
		return result, response, err
	}
	return result, response, nil
}

// QueryKV validates that request selects a key/value result, then executes and
// decodes it without requiring a caller-owned response struct.
func (c *Client) QueryKV(
	ctx context.Context,
	request querypkg.Request,
	options ...RequestOption,
) (querypkg.KVResult, *Response, error) {
	var result querypkg.KVResult
	if err := validateTypedQueryShape(request, querypkg.ShapeKV); err != nil {
		return result, nil, err
	}
	response, err := c.QueryRequest(ctx, request, &result, options...)
	if err != nil {
		return result, response, err
	}
	if err := validateTypedQueryResult(
		response, result.Shape, querypkg.ShapeKV, result.KV != nil,
	); err != nil {
		return result, response, err
	}
	return result, response, nil
}

// QueryColumnar validates that request selects a columnar result, then
// executes and decodes it without requiring a caller-owned response struct.
func (c *Client) QueryColumnar(
	ctx context.Context,
	request querypkg.Request,
	options ...RequestOption,
) (querypkg.ColumnarResult, *Response, error) {
	var result querypkg.ColumnarResult
	if err := validateTypedQueryShape(request, querypkg.ShapeColumnar); err != nil {
		return result, nil, err
	}
	response, err := c.QueryRequest(ctx, request, &result, options...)
	if err != nil {
		return result, response, err
	}
	if err := validateTypedQueryResult(
		response, result.Shape, querypkg.ShapeColumnar, result.Columnar != nil,
	); err != nil {
		return result, response, err
	}
	return result, response, nil
}

func validateTypedQueryShape(request querypkg.Request, expected querypkg.Shape) error {
	actual := request.Return.Shape()
	if actual == expected {
		return nil
	}
	if err := request.Validate(); err != nil {
		return err
	}
	return &querypkg.ValidationError{
		Code: querypkg.CodeResultShapeMismatch,
		Path: "return.shape",
		Detail: fmt.Sprintf(
			"Query%s requires %q, but the request selects %q",
			shapeName(expected), expected, actual,
		),
	}
}

func validateTypedQueryResult(
	response *Response,
	actual querypkg.Shape,
	expected querypkg.Shape,
	payloadPresent bool,
) error {
	requestID := ""
	if response != nil {
		requestID = response.RequestID
	}
	if actual != expected {
		return &ProtocolError{
			RequestID: requestID,
			Detail: fmt.Sprintf(
				"typed %s query received response shape %q", expected, actual,
			),
		}
	}
	if !payloadPresent {
		return &ProtocolError{
			RequestID: requestID,
			Detail: fmt.Sprintf(
				"typed %s query response omitted the %s payload", expected, expected,
			),
		}
	}
	return nil
}

func shapeName(shape querypkg.Shape) string {
	switch shape {
	case querypkg.ShapeTable:
		return "Table"
	case querypkg.ShapeGraph:
		return "Graph"
	case querypkg.ShapeDocument:
		return "Document"
	case querypkg.ShapeKV:
		return "KV"
	case querypkg.ShapeColumnar:
		return "Columnar"
	default:
		return "Typed"
	}
}
