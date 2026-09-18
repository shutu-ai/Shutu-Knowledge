//go:build corpusgen

package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", filepath.Join("testdata", "document_intelligence"), "corpus output directory")
	flag.Parse()
	if err := os.MkdirAll(filepath.Join(*out, "office"), 0o755); err != nil {
		panic(err)
	}
	pdfBodies := map[string]string{
		"simple.pdf":      "BT /F1 18 Tf 72 720 Td (Revenue Overview) Tj ET\nBT /F1 12 Tf 72 690 Td (APAC revenue Q4 150) Tj ET",
		"multicolumn.pdf": "BT /F1 18 Tf 72 720 Td (Left Column) Tj ET\nBT /F1 12 Tf 72 690 Td (Right Column) Tj ET",
		"tables.pdf":      "BT /F1 18 Tf 72 720 Td (Revenue Table) Tj ET\nBT /F1 12 Tf 72 690 Td (APAC | 150 | Q4) Tj ET",
		"figures.pdf":     "BT /F1 18 Tf 72 720 Td (Figure 1) Tj ET\nBT /F1 12 Tf 72 690 Td (Revenue trend caption) Tj ET",
		"scanned.pdf":     "BT /F1 12 Tf 72 690 Td (OCR fallback evidence) Tj ET",
		"long.pdf":        "BT /F1 18 Tf 72 720 Td (Long report) Tj ET\nBT /F1 12 Tf 72 690 Td (Page one bounded evidence) Tj ET",
	}
	for name, body := range pdfBodies {
		pages := []string{body}
		if name == "long.pdf" {
			pages = make([]string, 105)
			for page := range pages {
				pages[page] = fmt.Sprintf("BT /F1 18 Tf 72 720 Td (Long report section %d) Tj ET\nBT /F1 12 Tf 72 690 Td (Page %d bounded evidence) Tj ET", page+1, page+1)
			}
		}
		if err := os.WriteFile(filepath.Join(*out, name), pdfBytesPages(pages), 0o644); err != nil {
			panic(err)
		}
	}
	writeZip(filepath.Join(*out, "office", "structured.docx"), map[string]string{
		"word/document.xml": string(readSource(filepath.Join(*out, "office", "structured.docx.xml"))),
	})
	writeZip(filepath.Join(*out, "office", "tables.docx"), map[string]string{
		"word/document.xml": string(readSource(filepath.Join(*out, "office", "tables.docx.xml"))),
	})
	var slides = map[string]string{}
	for i := 1; i <= 8; i++ {
		body := fmt.Sprintf(`<p:sld xmlns:p="p" xmlns:a="a"><a:t>Slide %d APAC</a:t></p:sld>`, i)
		if i == 1 {
			body = `<p:sld xmlns:p="p" xmlns:a="a"><p:sp><a:t>Revenue title</a:t></p:sp><p:graphicFrame><a:tbl><a:tr><a:tc><a:t>Region</a:t></a:tc><a:tc><a:t>Q4</a:t></a:tc></a:tr></a:tbl></p:graphicFrame><p:pic/></p:sld>`
		} else if i == 3 {
			body = `<p:sld xmlns:p="p" xmlns:a="a"><p:sp><a:t>Churn and retention</a:t></p:sp><p:sp><a:t>Churn discussion and retention actions</a:t></p:sp></p:sld>`
		}
		slides[fmt.Sprintf("ppt/slides/slide%d.xml", i)] = body
	}
	slides["ppt/notesSlides/notesSlide1.xml"] = `<p:notes xmlns:p="p" xmlns:a="a"><a:t>Discuss churn assumptions</a:t></p:notes>`
	writeZip(filepath.Join(*out, "office", "presentation.pptx"), slides)
	writeZip(filepath.Join(*out, "office", "spreadsheet.xlsx"), map[string]string{
		"xl/workbook.xml":          string(readSource(filepath.Join(*out, "office", "workbook.xml"))),
		"xl/sharedStrings.xml":     string(readSource(filepath.Join(*out, "office", "sharedStrings.xml"))),
		"xl/worksheets/sheet1.xml": string(readSource(filepath.Join(*out, "office", "sheet1.xml"))),
	})
}

func readSource(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func writeZip(path string, files map[string]string) {
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	zw := zip.NewWriter(file)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := files[name]
		header := &zip.FileHeader{Name: name, Method: zip.Store, Modified: time.Unix(0, 0).UTC()}
		writer, err := zw.CreateHeader(header)
		if err != nil {
			panic(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}

func pdfBytes(content string) []byte {
	return pdfBytesPages([]string{content})
}

func pdfBytesPages(pages []string) []byte {
	if len(pages) == 0 {
		pages = []string{""}
	}
	pageCount := len(pages)
	pageObjectStart := 3
	contentObjectStart := pageObjectStart + pageCount
	fontObject := contentObjectStart + pageCount
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
	}
	kids := make([]string, 0, pageCount)
	for page := 0; page < pageCount; page++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjectStart+page))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pageCount))
	for page := 0; page < pageCount; page++ {
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontObject, contentObjectStart+page))
	}
	for _, content := range pages {
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := int64(out.Len())
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return out.Bytes()
}
