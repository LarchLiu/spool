package office

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"

	"github.com/xuri/excelize/v2"
)

func spreadsheet(ctx context.Context, source io.Reader, sheetIndex, start int) (*Document, error) {
	f, err := excelize.OpenReader(source, excelize.Options{UnzipSizeLimit: maxExpanded, UnzipXMLSizeLimit: maxExpanded})
	if err != nil {
		return nil, errors.New("This Excel workbook cannot be previewed")
	}
	defer f.Close()
	names := f.GetSheetList()
	doc := &Document{Kind: "spreadsheet", Sheet: sheetIndex, RowStart: start, Rows: []Row{}}
	if len(names) > 512 {
		return nil, errors.New("Workbook contains too many worksheets")
	}
	for i, name := range names {
		visible, _ := f.GetSheetVisible(name)
		if visible {
			doc.Sheets = append(doc.Sheets, Sheet{Index: i, Name: name})
		}
	}
	if len(doc.Sheets) == 0 {
		return nil, errors.New("Workbook has no visible worksheets")
	}
	selected := ""
	for _, s := range doc.Sheets {
		if s.Index == sheetIndex {
			selected = s.Name
		}
	}
	if selected == "" {
		if sheetIndex != 0 {
			return nil, errors.New("Worksheet is unavailable")
		}
		sheetIndex = doc.Sheets[0].Index
		doc.Sheet = sheetIndex
		selected = names[sheetIndex]
	}
	rows, err := f.Rows(selected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	styles := map[int]Style{}
	cellStyle := func(name string) Style {
		id, _ := f.GetCellStyle(selected, name)
		style, found := styles[id]
		if !found {
			original, err := f.GetStyle(id)
			if err == nil {
				style = excelStyle(original)
			}
			styles[id] = style
		}
		return style
	}
	textLeft, columns := maxText, 1
	if dimension, err := f.GetSheetDimension(selected); err == nil {
		for _, cell := range strings.Split(dimension, ":") {
			col, _, err := excelize.CellNameToCoordinates(cell)
			if err == nil {
				if col > maxColumns {
					doc.Truncated = true
				}
				columns = max(columns, min(maxColumns, col))
			}
		}
	}
	for number := 1; rows.Next(); number++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(doc.Rows) == PageRows {
			doc.HasMore = true
			break
		}
		values, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		if len(values) > maxColumns {
			values = values[:maxColumns]
			doc.Truncated = true
		}
		columns = max(columns, len(values))
		if number <= start {
			continue
		}
		row := Row{Number: number, Cells: []Cell{}}
		height, _ := f.GetRowHeight(selected, number)
		row.Height = max(28, min(320, int(math.Ceil(height*4/3))))
		for col, value := range values {
			cellName, _ := excelize.CoordinatesToCellName(col+1, number)
			if value == "" {
				formula, _ := f.GetCellFormula(selected, cellName)
				if formula != "" {
					value = "=" + formula
				}
			}
			if len(value) > 4096 {
				value = clip(value, 4096)
				doc.Truncated = true
			}
			if len(value) > textLeft {
				value = clip(value, textLeft)
				doc.Truncated = true
			}
			textLeft -= len(value)
			row.Cells = append(row.Cells, Cell{Text: value, Style: cellStyle(cellName)})
		}
		doc.Rows = append(doc.Rows, row)
	}
	if err := rows.Error(); err != nil {
		return nil, err
	}
	merges, err := f.GetMergeCells(selected, true)
	if err != nil {
		return nil, err
	}
	if len(merges) > 8192 {
		return nil, errors.New("Worksheet contains too many merged regions")
	}
	// A merge can extend beyond the last explicit <row>. Keep its continuation
	// available even when the row iterator has already reached the end of the XML.
	lastMergeRow := 0
	for _, merge := range merges {
		_, end, err := excelize.CellNameToCoordinates(merge.GetEndAxis())
		if err == nil {
			lastMergeRow = max(lastMergeRow, end)
		}
	}
	for len(doc.Rows) < PageRows && start+len(doc.Rows) < lastMergeRow {
		number := start + len(doc.Rows) + 1
		height, _ := f.GetRowHeight(selected, number)
		doc.Rows = append(doc.Rows, Row{Number: number, Height: max(28, min(320, int(math.Ceil(height*4/3)))), Cells: []Cell{}})
	}
	doc.HasMore = doc.HasMore || lastMergeRow > start+len(doc.Rows)
	type region struct {
		x1, y1, x2, y2 int
		text           string
		style          Style
	}
	regions := []region{}
	for _, merge := range merges {
		x1, y1, e1 := excelize.CellNameToCoordinates(merge.GetStartAxis())
		x2, y2, e2 := excelize.CellNameToCoordinates(merge.GetEndAxis())
		if e1 != nil || e2 != nil || y2 <= start || y1 > start+len(doc.Rows) || x1 > maxColumns {
			continue
		}
		if x2 > maxColumns {
			doc.Truncated = true
		}
		x2 = min(maxColumns, x2)
		columns = max(columns, x2)
		value, _ := f.GetCellValue(selected, merge.GetStartAxis())
		if len(value) > 4096 {
			value = clip(value, 4096)
			doc.Truncated = true
		}
		if len(value) > textLeft {
			value = clip(value, textLeft)
			doc.Truncated = true
		}
		textLeft -= len(value)
		regions = append(regions, region{x1, y1, x2, y2, value, cellStyle(merge.GetStartAxis())})
	}
	for i := range doc.Rows {
		for len(doc.Rows[i].Cells) < columns {
			doc.Rows[i].Cells = append(doc.Rows[i].Cells, Cell{})
		}
	}
	for _, m := range regions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		first, last := max(start+1, m.y1), min(start+len(doc.Rows), m.y2)
		for row := first; row <= last; row++ {
			for col := m.x1; col <= m.x2; col++ {
				cell := &doc.Rows[row-start-1].Cells[col-1]
				if row == first && col == m.x1 {
					cell.Text = m.text
					cell.Style = m.style
					cell.ColSpan = m.x2 - m.x1 + 1
					cell.RowSpan = last - first + 1
				} else {
					cell.Hidden = true
				}
			}
		}
	}
	for col := 1; col <= columns; col++ {
		name, _ := excelize.ColumnNumberToName(col)
		width, _ := f.GetColWidth(selected, name)
		doc.Columns = append(doc.Columns, max(64, min(320, int(math.Round(width*7+5)))))
	}
	return doc, nil
}

func excelStyle(s *excelize.Style) Style {
	style := Style{}
	if s.Font != nil {
		style.Bold = s.Font.Bold
		style.Italic = s.Font.Italic
		style.Underline = s.Font.Underline != "" && s.Font.Underline != "none"
		style.Color = color(s.Font.Color)
	}
	if len(s.Fill.Color) > 0 && s.Fill.Pattern != 0 {
		style.Background = color(s.Fill.Color[0])
	}
	if s.Alignment != nil {
		style.Align = align(s.Alignment.Horizontal)
	}
	return style
}
