package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func office(parts map[string]string) []byte {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, value := range parts {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(value))
	}
	_ = z.Close()
	return out.Bytes()
}
func TestExtractCSVPreservesRowLocator(t *testing.T) {
	x, err := Extract(context.Background(), "text/csv", []byte("name,value\nalpha,42\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Chunks) != 2 || x.Chunks[1].Locator["row"] != 2 || x.Chunks[1].Text != "alpha | 42" {
		t.Fatalf("unexpected extraction: %+v", x)
	}
}
func TestExtractDOCXPreservesParagraphLocator(t *testing.T) {
	data := office(map[string]string{"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p><w:p><w:r><w:t>World</w:t></w:r></w:p></w:body></w:document>`})
	x, err := Extract(context.Background(), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Chunks) != 2 || x.Chunks[1].Text != "World" {
		t.Fatalf("unexpected extraction: %+v", x)
	}
}
func TestExtractXLSXUsesCachedValuesOnly(t *testing.T) {
	data := office(map[string]string{"xl/sharedStrings.xml": `<sst><si><t>Name</t></si></sst>`, "xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1"><f>1+1</f><v>2</v></c></row></sheetData></worksheet>`})
	x, err := Extract(context.Background(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Chunks) != 1 || x.Chunks[0].Text != "A1=Name | B1=2" {
		t.Fatalf("unexpected extraction: %+v", x)
	}
}
func TestRejectsUnsupportedAndEmptyDocuments(t *testing.T) {
	if _, err := Extract(context.Background(), "text/plain", []byte("x")); err == nil {
		t.Fatal("unsupported type accepted")
	}
	if _, err := Extract(context.Background(), "text/csv", []byte("\n")); err == nil {
		t.Fatal("empty document accepted")
	}
}

func TestDOCXRejectsMoreThanMaximumChunks(t *testing.T) {
	var xml bytes.Buffer
	xml.WriteString(`<w:document xmlns:w="w"><w:body>`)
	for i := 0; i <= maxChunks; i++ {
		xml.WriteString(`<w:p><w:r><w:t>x</w:t></w:r></w:p>`)
	}
	xml.WriteString(`</w:body></w:document>`)
	_, err := extractDOCX(office(map[string]string{"word/document.xml": xml.String()}))
	if err == nil || err.Error() != "document_too_many_chunks" {
		t.Fatalf("got %v, want document_too_many_chunks", err)
	}
}

func TestXLSXRejectsSheetAndCellLimitsBeforeChunking(t *testing.T) {
	tooManySheets := map[string]string{}
	for i := 1; i <= 11; i++ {
		tooManySheets[fmt.Sprintf("xl/worksheets/sheet%d.xml", i)] = `<worksheet><sheetData><row r="1"><c r="A1"><v>1</v></c></row></sheetData></worksheet>`
	}
	if _, err := extractXLSX(office(tooManySheets)); err == nil || err.Error() != "xlsx_too_many_sheets" {
		t.Fatalf("sheet limit: got %v", err)
	}

	var cells strings.Builder
	cells.WriteString(`<worksheet><sheetData><row r="1">`)
	for i := 0; i <= 50000; i++ {
		fmt.Fprintf(&cells, `<c r="A%d"><v>1</v></c>`, i+1)
	}
	cells.WriteString(`</row></sheetData></worksheet>`)
	if _, err := extractXLSX(office(map[string]string{"xl/worksheets/sheet1.xml": cells.String()})); err == nil || err.Error() != "xlsx_too_many_cells" {
		t.Fatalf("cell limit: got %v", err)
	}
}

func TestPDFTextPreservesSourcePageLocators(t *testing.T) {
	chunks, err := splitPDFText("First page\fSecond page")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0].Locator["page"] != 1 || chunks[1].Locator["page"] != 2 {
		t.Fatalf("unexpected PDF locators: %+v", chunks)
	}
}

func TestReadLimitedFileRejectsOversizedParserOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(path, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLimitedFile(path, 3); err == nil || err.Error() != "document_text_too_large" {
		t.Fatalf("got %v, want document_text_too_large", err)
	}
}

func TestParsePDFPageCountRequiresPositivePages(t *testing.T) {
	if pages, err := parsePDFPageCount([]byte("Title: sample\nPages:          201\n")); err != nil || pages != 201 {
		t.Fatalf("pages=%d err=%v", pages, err)
	}
	for _, info := range []string{"Pages: 0\n", "Pages: many\n", "Title: no count\n"} {
		if _, err := parsePDFPageCount([]byte(info)); err == nil {
			t.Fatalf("invalid page count accepted: %q", info)
		}
	}
}

func TestPDFPageLimitsRejectInsteadOfTruncate(t *testing.T) {
	for _, test := range []struct {
		pages int
		ocr   bool
		ok    bool
	}{
		{pages: 200, ok: true},
		{pages: 201, ok: false},
		{pages: 50, ocr: true, ok: true},
		{pages: 51, ocr: true, ok: false},
	} {
		err := validatePDFPageLimit(test.pages, test.ocr)
		if (err == nil) != test.ok {
			t.Fatalf("pages=%d ocr=%v err=%v", test.pages, test.ocr, err)
		}
	}
}
