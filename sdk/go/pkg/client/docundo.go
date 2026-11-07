package client

// Undo for live documents: delete, the document trash, and the text kept from
// before a compaction. Deleted documents stay restorable for the space's
// history TTL; a space with history_ttl_sec 0 keeps no trash.

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/ab0t-com/acp/sdk/go/internal/wire"
)

// ErrNoDocUndo reports a server too old to keep deleted documents or earlier
// versions (it does not have the routes). Nothing was changed.
var ErrNoDocUndo = errors.New("this server does not keep deleted documents or earlier versions; it needs ACP v0.2.6 or newer")

// DocVersionText is the text of one retained pre-compaction version.
type DocVersionText struct {
	Doc   string `json:"doc"`
	Epoch int    `json:"epoch"`
	At    int    `json:"at"`  // the operation count this text is the result of
	Ops   int    `json:"ops"` // how many operations the version holds
	Text  string `json:"text"`
}

// docPath is the route prefix of one document kind.
func docPath(isJSON bool) string {
	if isJSON {
		return "/v1/crdt/json"
	}
	return "/v1/crdt"
}

// DeleteDoc deletes a live text document. It returns the trash entry that can
// bring it back, or nil when the space keeps no trash (or the server is older
// than v0.2.6). A name that holds no document is a 404 APIError.
func (c *Client) DeleteDoc(doc string) (*wire.DocTrashEntry, error) {
	return c.deleteDoc(false, doc)
}

// DeleteJSONDoc is DeleteDoc for a structured document.
func (c *Client) DeleteJSONDoc(doc string) (*wire.DocTrashEntry, error) {
	return c.deleteDoc(true, doc)
}

func (c *Client) deleteDoc(isJSON bool, doc string) (*wire.DocTrashEntry, error) {
	if doc == "" {
		return nil, fmt.Errorf("doc delete: a document name is required")
	}
	var out struct {
		Trash *wire.DocTrashEntry `json:"trash"`
	}
	err := c.do("DELETE", docPath(isJSON)+"/doc?doc="+url.QueryEscape(doc), nil, &out)
	return out.Trash, err
}

// DocTrash lists the deleted text documents that can be restored, oldest first.
func (c *Client) DocTrash() ([]wire.DocTrashEntry, error) { return c.docTrash(false) }

// JSONDocTrash lists the deleted structured documents that can be restored.
func (c *Client) JSONDocTrash() ([]wire.DocTrashEntry, error) { return c.docTrash(true) }

func (c *Client) docTrash(isJSON bool) ([]wire.DocTrashEntry, error) {
	var out struct {
		Trash []wire.DocTrashEntry `json:"trash"`
	}
	if err := c.do("GET", docPath(isJSON)+"/trash", nil, &out); err != nil {
		return nil, undoRouteErr(err)
	}
	if out.Trash == nil {
		out.Trash = []wire.DocTrashEntry{}
	}
	return out.Trash, nil
}

// RestoreDoc brings the deleted text document doc at generation gen back under
// its name. Refusals: 404 trash_not_found, 409 doc_exists (a live document
// holds the name), 507 at the document quota.
func (c *Client) RestoreDoc(doc string, gen int) error { return c.restoreDoc(false, doc, gen) }

// RestoreJSONDoc is RestoreDoc for a structured document.
func (c *Client) RestoreJSONDoc(doc string, gen int) error { return c.restoreDoc(true, doc, gen) }

func (c *Client) restoreDoc(isJSON bool, doc string, gen int) error {
	if doc == "" || gen < 0 {
		return fmt.Errorf("doc restore: a document name and a generation from the trash listing are required")
	}
	return undoRouteErr(c.do("POST", docPath(isJSON)+"/trash/restore", map[string]any{"doc": doc, "gen": gen}, nil))
}

// DocVersions lists the retained pre-compaction versions of a text document,
// oldest first. No versions is an empty list, also for a document outside the
// token's read scope.
func (c *Client) DocVersions(doc string) ([]wire.DocVersionEntry, error) {
	var out struct {
		Versions []wire.DocVersionEntry `json:"versions"`
	}
	if err := c.do("GET", "/v1/crdt/versions?doc="+url.QueryEscape(doc), nil, &out); err != nil {
		return nil, undoRouteErr(err)
	}
	if out.Versions == nil {
		out.Versions = []wire.DocVersionEntry{}
	}
	return out.Versions, nil
}

// DocVersionAt reads a text document as it was at epoch: the text after the
// first at operations of that version, or the whole version when at is 0.
func (c *Client) DocVersionAt(doc string, epoch, at int) (DocVersionText, error) {
	var out DocVersionText
	if epoch < 0 || at < 0 {
		return out, fmt.Errorf("doc version: epoch and at must not be negative")
	}
	q := "/v1/crdt/versions?doc=" + url.QueryEscape(doc) + "&epoch=" + strconv.Itoa(epoch)
	if at > 0 {
		q += "&at=" + strconv.Itoa(at)
	}
	return out, undoRouteErr(c.do("GET", q, nil, &out))
}

// undoRouteErr turns the answer of a server without these routes into ErrNoDocUndo: a
// 404 unknown_route, or an older server's 404 with no code (every refusal of the real
// routes carries one). Any other code is the route's own answer and goes through as is.
func undoRouteErr(err error) error {
	if IsRouteMissing(err) {
		return ErrNoDocUndo
	}
	return err
}
