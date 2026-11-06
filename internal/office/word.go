package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"path"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type element struct {
	name     string
	attrs    map[string]string
	text     string
	children []*element
}

func parseXML(ctx context.Context, data []byte) (*element, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root := &element{}
	stack := []*element{root}
	nodes := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			nodes++
			if nodes > 150000 || len(stack) > 64 {
				return nil, errors.New("Office XML exceeds the preview complexity limit")
			}
			n := &element{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			n := stack[len(stack)-1]
			n.text += string(t)
		}
	}
	if len(root.children) != 1 {
		return nil, errors.New("Invalid Office XML document")
	}
	return root.children[0], nil
}

func (n *element) child(name string) *element {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return &element{}
}
func (n *element) walk(visit func(*element)) {
	visit(n)
	for _, c := range n.children {
		c.walk(visit)
	}
}

type wordReader struct {
	parts                             map[string]*zip.File
	rels                              map[string]string
	styles                            map[string]*element
	doc                               *Document
	textLeft, imageBytes, imagePixels int
	images                            map[string]string
	imageSizes                        map[string]int
}

func word(ctx context.Context, parts map[string]*zip.File) (*Document, error) {
	data, err := readPart(parts["word/document.xml"], maxXML)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(ctx, data)
	if err != nil || root.name != "document" {
		return nil, errors.New("Invalid Word document")
	}
	r := &wordReader{parts: parts, rels: map[string]string{}, styles: map[string]*element{}, doc: &Document{Kind: "word", Blocks: []Block{}}, textLeft: maxText, images: map[string]string{}, imageSizes: map[string]int{}}
	if f := parts["word/_rels/document.xml.rels"]; f != nil {
		data, err := readPart(f, maxXML)
		if err != nil {
			return nil, err
		}
		rels, err := parseXML(ctx, data)
		if err != nil {
			return nil, err
		}
		for _, rel := range rels.children {
			if rel.name == "Relationship" && rel.attrs["TargetMode"] != "External" {
				target := path.Clean(path.Join("word", rel.attrs["Target"]))
				if strings.HasPrefix(rel.attrs["Target"], "/") {
					target = path.Clean(strings.TrimPrefix(rel.attrs["Target"], "/"))
				}
				if strings.HasPrefix(target, "word/media/") {
					r.rels[rel.attrs["Id"]] = target
				}
			}
		}
	}
	if f := parts["word/styles.xml"]; f != nil {
		data, err := readPart(f, maxXML)
		if err != nil {
			return nil, err
		}
		styles, err := parseXML(ctx, data)
		if err != nil {
			return nil, err
		}
		for _, s := range styles.children {
			if s.name == "style" {
				r.styles[s.attrs["styleId"]] = s
			}
		}
	}
	for _, n := range root.child("body").children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(r.doc.Blocks) >= 2000 || r.textLeft == 0 {
			r.doc.Truncated = true
			break
		}
		switch n.name {
		case "p":
			r.doc.Blocks = append(r.doc.Blocks, r.paragraph(n))
		case "tbl":
			r.doc.Blocks = append(r.doc.Blocks, r.table(n))
		}
	}
	return r.doc, nil
}

func (r *wordReader) text(value string) string {
	if len(value) > r.textLeft {
		value = clip(value, r.textLeft)
		r.doc.Truncated = true
	}
	r.textLeft -= len(value)
	return value
}

func properties(n *element, style Style) Style {
	for _, v := range n.children {
		on := v.attrs["val"] != "0" && v.attrs["val"] != "false" && v.attrs["val"] != "off"
		switch v.name {
		case "b":
			style.Bold = on
		case "i":
			style.Italic = on
		case "u":
			style.Underline = on && v.attrs["val"] != "none"
		case "color":
			style.Color = color(v.attrs["val"])
		case "shd":
			style.Background = color(v.attrs["fill"])
		}
	}
	return style
}

func (r *wordReader) paragraph(n *element) Block {
	p := n.child("pPr")
	block := Block{Kind: "paragraph", Align: align(p.child("jc").attrs["val"]), Runs: []Run{}}
	styleID := p.child("pStyle").attrs["val"]
	style := Style{}
	if s := r.styles[styleID]; s != nil {
		style = properties(s.child("rPr"), style)
		if block.Align == "" {
			block.Align = align(s.child("pPr").child("jc").attrs["val"])
		}
	}
	level := p.child("outlineLvl").attrs["val"]
	if level == "" && r.styles[styleID] != nil {
		level = r.styles[styleID].child("pPr").child("outlineLvl").attrs["val"]
	}
	if v, err := strconv.Atoi(level); err == nil && v < 6 && v >= 0 {
		block.Level = v + 1
	}
	if strings.HasPrefix(strings.ToLower(styleID), "heading") {
		if v, err := strconv.Atoi(styleID[7:]); err == nil && v >= 1 && v <= 6 {
			block.Level = v
		}
	}
	if p.child("numPr").name != "" {
		block.Runs = append(block.Runs, Run{Text: "• "})
	}
	n.walk(func(node *element) {
		if node.name != "r" {
			return
		}
		runStyle := properties(node.child("rPr"), style)
		for _, c := range node.children {
			switch c.name {
			case "t":
				block.Runs = append(block.Runs, Run{Text: r.text(c.text), Style: runStyle})
			case "tab":
				block.Runs = append(block.Runs, Run{Text: r.text("\t"), Style: runStyle})
			case "br", "cr":
				block.Runs = append(block.Runs, Run{Text: r.text("\n"), Style: runStyle})
			case "drawing", "pict":
				alt := "Image"
				c.walk(func(v *element) {
					if v.name == "docPr" && v.attrs["descr"] != "" {
						alt = v.attrs["descr"]
					}
				})
				c.walk(func(v *element) {
					id := v.attrs["embed"]
					if v.name == "imagedata" {
						id = v.attrs["id"]
					}
					if id == "" {
						return
					}
					image := r.image(r.rels[id])
					block.Runs = append(block.Runs, Run{Image: image, Text: map[bool]string{true: "", false: "[" + alt + "]"}[image != ""], Alt: alt})
				})
			}
		}
	})
	return block
}

func (r *wordReader) table(n *element) Block {
	block := Block{Kind: "table"}
	for _, row := range n.children {
		if row.name != "tr" {
			continue
		}
		if len(block.Rows) >= 200 {
			r.doc.Truncated = true
			break
		}
		cells := []Cell{}
		for _, cell := range row.children {
			if cell.name != "tc" {
				continue
			}
			if len(cells) >= 64 {
				r.doc.Truncated = true
				break
			}
			span, _ := strconv.Atoi(cell.child("tcPr").child("gridSpan").attrs["val"])
			span = max(1, min(64, span))
			value := Cell{ColSpan: span}
			var text []string
			cell.walk(func(p *element) {
				if p.name != "p" {
					return
				}
				paragraph := r.paragraph(p)
				var line strings.Builder
				for _, run := range paragraph.Runs {
					line.WriteString(run.Text)
					if run.Image != "" {
						line.WriteString("[" + run.Alt + "]")
					}
				}
				text = append(text, line.String())
				if len(text) == 1 && len(paragraph.Runs) > 0 {
					value.Style = paragraph.Runs[0].Style
				}
			})
			value.Text = strings.Join(text, "\n")
			value.Style.Background = color(cell.child("tcPr").child("shd").attrs["fill"])
			cells = append(cells, value)
		}
		block.Rows = append(block.Rows, cells)
	}
	return block
}

func (r *wordReader) image(name string) string {
	if value, ok := r.images[name]; ok {
		if r.imageBytes+len(value) > 2<<20 || r.imagePixels+r.imageSizes[name] > 4_000_000 {
			r.doc.Truncated = true
			return ""
		}
		r.imageBytes += len(value)
		r.imagePixels += r.imageSizes[name]
		return value
	}
	r.images[name] = ""
	if name == "" || len(r.images) > 32 {
		r.doc.Truncated = true
		return ""
	}
	data, err := readPart(r.parts[name], 8<<20)
	if err != nil {
		return ""
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return ""
	}
	value, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	w, h := config.Width, config.Height
	if max(w, h) > 1024 {
		w, h = max(1, w*1024/max(w, h)), max(1, h*1024/max(w, h))
	}
	if r.imagePixels+w*h > 4_000_000 {
		r.doc.Truncated = true
		return ""
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.BiLinear.Scale(thumb, thumb.Bounds(), value, value.Bounds(), draw.Src, nil)
	var output bytes.Buffer
	if png.Encode(&output, thumb) != nil {
		return ""
	}
	if r.imageBytes+output.Len() > 2<<20 {
		r.doc.Truncated = true
		return ""
	}
	r.images[name] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
	r.imageBytes += len(r.images[name])
	r.imagePixels += w * h
	r.imageSizes[name] = w * h
	return r.images[name]
}
