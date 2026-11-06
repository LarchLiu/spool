// Package office produces bounded, read-only OOXML previews without external programs.
package office

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	MaxFileBytes = 32 << 20
	maxExpanded  = 64 << 20
	maxXML       = 16 << 20
	maxText      = 256 << 10
	PageRows     = 50
	maxColumns   = 128
)

type Style struct {
	Bold       bool   `json:"bold,omitempty"`
	Italic     bool   `json:"italic,omitempty"`
	Underline  bool   `json:"underline,omitempty"`
	Color      string `json:"color,omitempty"`
	Background string `json:"background,omitempty"`
	Align      string `json:"align,omitempty"`
}
type Run struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	Alt   string `json:"alt,omitempty"`
	Style Style  `json:"style,omitempty"`
}
type Cell struct {
	Text    string `json:"text"`
	Style   Style  `json:"style,omitempty"`
	ColSpan int    `json:"col_span,omitempty"`
	RowSpan int    `json:"row_span,omitempty"`
	Hidden  bool   `json:"hidden,omitempty"`
}
type Block struct {
	Kind  string   `json:"kind"`
	Level int      `json:"level,omitempty"`
	Align string   `json:"align,omitempty"`
	Runs  []Run    `json:"runs,omitempty"`
	Rows  [][]Cell `json:"rows,omitempty"`
}
type Sheet struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}
type Row struct {
	Number int    `json:"number"`
	Height int    `json:"height,omitempty"`
	Cells  []Cell `json:"cells"`
}
type Document struct {
	Kind      string  `json:"kind"`
	Blocks    []Block `json:"blocks,omitempty"`
	Sheets    []Sheet `json:"sheets,omitempty"`
	Sheet     int     `json:"sheet"`
	Columns   []int   `json:"columns,omitempty"`
	Rows      []Row   `json:"rows,omitempty"`
	RowStart  int     `json:"row_start"`
	HasMore   bool    `json:"has_more"`
	Truncated bool    `json:"truncated"`
}

func Kind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".docx":
		return "word"
	case ".xlsx":
		return "spreadsheet"
	}
	return ""
}

var slots = make(chan struct{}, 2)

func Preview(ctx context.Context, source io.ReaderAt, size int64, name string, sheet, row int) (*Document, error) {
	if size <= 0 || size > MaxFileBytes {
		return nil, errors.New("Office file exceeds the preview size limit")
	}
	if sheet < 0 || row < 0 || row >= 1048576 {
		return nil, errors.New("Invalid worksheet or row")
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return nil, errors.New("This file is not a supported OOXML document")
	}
	if len(reader.File) > 8192 {
		return nil, errors.New("Office archive contains too many parts")
	}
	parts := make(map[string]*zip.File, len(reader.File))
	var expanded uint64
	for _, f := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > maxExpanded || expanded > maxExpanded-f.UncompressedSize64 {
			return nil, errors.New("Office archive exceeds the preview size limit")
		}
		expanded += f.UncompressedSize64
		if _, duplicate := parts[f.Name]; duplicate {
			return nil, errors.New("Office archive contains duplicate parts")
		}
		parts[f.Name] = f
	}
	if parts["[Content_Types].xml"] == nil {
		return nil, errors.New("Missing Office content types")
	}
	switch Kind(name) {
	case "word":
		return word(ctx, parts)
	case "spreadsheet":
		if parts["xl/workbook.xml"] == nil {
			return nil, errors.New("Missing Excel workbook")
		}
		return spreadsheet(ctx, io.NewSectionReader(source, 0, size), sheet, row)
	}
	return nil, errors.New("Only .docx and .xlsx previews are supported")
}

func readPart(f *zip.File, limit int64) ([]byte, error) {
	if f == nil {
		return nil, errors.New("Missing Office part")
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("Office part exceeds the preview size limit")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("Office part exceeds the preview size limit")
	}
	return data, nil
}

func color(value string) string {
	value = strings.TrimPrefix(value, "#")
	if len(value) == 8 {
		value = value[2:]
	}
	if len(value) != 6 {
		return ""
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return ""
		}
	}
	return "#" + value
}

func align(value string) string {
	switch value {
	case "left", "center", "right", "justify":
		return value
	}
	return ""
}

func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
