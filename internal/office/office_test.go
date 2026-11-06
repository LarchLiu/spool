package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	imagecolor "image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func archive(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, value := range parts {
		f, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func wordFixture(t *testing.T) []byte {
	t.Helper()
	bitmap := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			bitmap.Set(x, y, imagecolor.RGBA{200, 40, 60, 255})
		}
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, bitmap); err != nil {
		t.Fatal(err)
	}
	return archive(t, map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Office 预览</w:t></w:r></w:p>
<w:p><w:r><w:t>正文 </w:t></w:r><w:r><w:rPr><w:b/><w:color w:val="2456AA"/></w:rPr><w:t>Bold</w:t></w:r><w:r><w:rPr><w:i/><w:u w:val="single"/></w:rPr><w:t> italic</w:t><w:br/><w:t>&lt;script&gt;literal&lt;/script&gt;</w:t></w:r></w:p>
<w:p><w:r><w:drawing><wp:docPr descr="红色图片"/><a:blip r:embed="image1"/></w:drawing></w:r></w:p>
<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:shd w:fill="E8EEF8"/></w:tcPr><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>合并标题</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>名称</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>数值</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`,
		"word/_rels/document.xml.rels": `<Relationships><Relationship Id="image1" Target="media/image.png"/><Relationship Id="external" Target="https://example.invalid/private.png" TargetMode="External"/></Relationships>`,
		"word/media/image.png":         picture.String(),
	})
}

func workbookFixture(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "明细"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Summary", "Empty", "Hidden"} {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SetSheetVisible("Hidden", false); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("明细", "A1", "销售统计"); err != nil {
		t.Fatal(err)
	}
	if err := f.MergeCell("明细", "A1", "B1"); err != nil {
		t.Fatal(err)
	}
	style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2456AA"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellStyle("明细", "A1", "B1", style); err != nil {
		t.Fatal(err)
	}
	for row := 2; row <= 122; row++ {
		cell, _ := excelize.CoordinatesToCellName(1, row)
		if err = f.SetCellValue("明细", cell, row); err != nil {
			t.Fatal(err)
		}
		cell, _ = excelize.CoordinatesToCellName(2, row)
		if err = f.SetCellValue("明细", cell, 2.5); err != nil {
			t.Fatal(err)
		}
	}
	format := "$0.00"
	currency, err := f.NewStyle(&excelize.Style{CustomNumFmt: &format})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellStyle("明细", "B2", "B122", currency); err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellFormula("明细", "C2", "SUM(A2:B2)"); err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellValue("明细", "H2", "横向滚动 / Horizontal scroll"); err != nil {
		t.Fatal(err)
	}
	if err = f.SetColWidth("明细", "H", "H", 28); err != nil {
		t.Fatal(err)
	}
	if err = f.MergeCell("明细", "A49", "A52"); err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellValue("Summary", "A1", "Summary worksheet"); err != nil {
		t.Fatal(err)
	}
	if err = f.SetCellValue("Hidden", "A1", "hidden-data"); err != nil {
		t.Fatal(err)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func preview(t *testing.T, data []byte, name string, sheet, row int) *Document {
	t.Helper()
	value, err := Preview(context.Background(), bytes.NewReader(data), int64(len(data)), name, sheet, row)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestOOXMLPreviewsAndSharedClientFixtures(t *testing.T) {
	word := preview(t, wordFixture(t), "报告.DOCX", 0, 0)
	if len(word.Blocks) != 4 || word.Blocks[0].Level != 1 || word.Blocks[0].Runs[0].Text != "Office 预览" || !word.Blocks[1].Runs[1].Style.Bold || !strings.HasPrefix(word.Blocks[2].Runs[0].Image, "data:image/png;base64,") || word.Blocks[3].Rows[0][0].ColSpan != 2 {
		t.Fatalf("Word structure: %+v", word)
	}
	xlsx := workbookFixture(t)
	first := preview(t, xlsx, "book.xlsx", 0, 0)
	second := preview(t, xlsx, "book.xlsx", 0, 50)
	summary := preview(t, xlsx, "book.xlsx", 1, 0)
	empty := preview(t, xlsx, "book.xlsx", 2, 0)
	if len(first.Rows) != 50 || !first.HasMore || len(first.Sheets) != 3 || first.Rows[0].Cells[0].ColSpan != 2 || !first.Rows[0].Cells[0].Style.Bold || first.Rows[1].Cells[1].Text != "$2.50" || first.Rows[1].Cells[2].Text != "=SUM(A2:B2)" {
		t.Fatalf("Excel first page: %+v", first)
	}
	if second.Rows[0].Number != 51 || second.Rows[0].Cells[0].Text != "49" || second.Rows[0].Cells[0].RowSpan != 2 || !second.Rows[1].Cells[0].Hidden {
		t.Fatalf("Merged page boundary: %+v", second.Rows[:2])
	}
	if len(empty.Rows) != 0 || empty.HasMore || summary.Rows[0].Cells[0].Text != "Summary worksheet" {
		t.Fatal("Sheet selection or empty sheet failed")
	}
	last := preview(t, xlsx, "book.xlsx", 0, 100)
	if len(last.Rows) != 22 || last.HasMore {
		t.Fatal("Last page incorrectly advertised more rows")
	}
	fixture := map[string]any{"word": word, "sheets": []*Document{first, second, summary, empty}}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	file := filepath.Join("..", "..", "android", "app", "src", "test", "resources", "office-preview.json")
	if os.Getenv("UPDATE_OFFICE_FIXTURES") == "1" {
		if err = os.WriteFile(file, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, encoded) {
		t.Fatal("Client fixtures differ from real Go previews; regenerate with UPDATE_OFFICE_FIXTURES=1")
	}
}

func TestOfficeRejectsMalformedLegacyOversizeAndCanceledRequests(t *testing.T) {
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"fake.docx", []byte("<html>fake</html>")}, {"old.doc", wordFixture(t)}, {"old.xls", workbookFixture(t)}, {"fake.xlsx", wordFixture(t)},
	} {
		if _, err := Preview(context.Background(), bytes.NewReader(item.data), int64(len(item.data)), item.name, 0, 0); err == nil {
			t.Error("Accepted", item.name)
		}
	}
	if _, err := Preview(context.Background(), bytes.NewReader(nil), MaxFileBytes+1, "big.docx", 0, 0); err == nil {
		t.Fatal("Missing file size budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := wordFixture(t)
	if _, err := Preview(ctx, bytes.NewReader(data), int64(len(data)), "a.docx", 0, 0); err == nil {
		t.Fatal("Canceled request continued")
	}
	for _, bounds := range [][2]int{{-1, 0}, {0, -1}, {0, 1048576}, {10, 0}, {3, 0}} {
		data := workbookFixture(t)
		if _, err := Preview(context.Background(), bytes.NewReader(data), int64(len(data)), "a.xlsx", bounds[0], bounds[1]); err == nil {
			t.Error("Accepted invalid/hidden sheet", bounds)
		}
	}
}

func TestWordExternalImagesAndXMLComplexityStayBounded(t *testing.T) {
	data := archive(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": `<document><body><p><r><drawing><blip embed="external"/></drawing></r></p></body></document>`, "word/_rels/document.xml.rels": `<Relationships><Relationship Id="external" Target="https://example.invalid/private" TargetMode="External"/></Relationships>`})
	doc := preview(t, data, "a.docx", 0, 0)
	if doc.Blocks[0].Runs[0].Image != "" || strings.Contains(doc.Blocks[0].Runs[0].Text, "https://") {
		t.Fatal("External image became preview input")
	}
	deep := archive(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": strings.Repeat("<document>", 70) + strings.Repeat("</document>", 70)})
	if _, err := Preview(context.Background(), bytes.NewReader(deep), int64(len(deep)), "a.docx", 0, 0); err == nil {
		t.Fatal("Missing XML depth limit")
	}
	absoluteImage := wordFixture(t)
	reader, err := zip.NewReader(bytes.NewReader(absoluteImage), int64(len(absoluteImage)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, part := range reader.File {
		data, err := readPart(part, maxXML)
		if err != nil {
			t.Fatal(err)
		}
		parts[part.Name] = string(data)
	}
	parts["word/_rels/document.xml.rels"] = `<Relationships><Relationship Id="image1" Target="/word/media/image.png"/></Relationships>`
	if doc := preview(t, archive(t, parts), "absolute.docx", 0, 0); doc.Blocks[2].Runs[0].Image == "" {
		t.Fatal("Package-absolute image relationship was not resolved")
	}
}

func TestSpreadsheetMergedPageRetainsOriginStyle(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetCellValue("Sheet1", "A49", "Continued group"); err != nil {
		t.Fatal(err)
	}
	if err := f.MergeCell("Sheet1", "A49", "B52"); err != nil {
		t.Fatal(err)
	}
	style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2456AA"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStyle("Sheet1", "A49", "A49", style); err != nil {
		t.Fatal(err)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	first := preview(t, buffer.Bytes(), "merged.xlsx", 0, 0)
	if len(first.Rows) != 50 || !first.HasMore {
		t.Fatal("Merge beyond the final explicit row did not provide a next page")
	}
	doc := preview(t, buffer.Bytes(), "merged.xlsx", 0, 50)
	if len(doc.Rows) != 2 {
		t.Fatalf("Missing merged continuation rows: %+v", doc.Rows)
	}
	cell := doc.Rows[0].Cells[0]
	if cell.Text != "Continued group" || cell.RowSpan != 2 || cell.ColSpan != 2 || !cell.Style.Bold || cell.Style.Background != "#2456AA" || cell.Style.Color != "#FFFFFF" {
		t.Fatalf("Lost merge origin content or style: %+v", cell)
	}
}
