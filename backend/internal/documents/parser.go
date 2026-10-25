package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxExtractedBytes    = 50 << 20
	maxArchiveBytes      = 100 << 20
	maxChunks            = 5000
	maxSpreadsheetSheets = 10
	maxSpreadsheetCells  = 50000
	maxPDFTextPages      = 200
	maxPDFOCRPages       = 50
)

type Chunk struct {
	Locator map[string]any `json:"locator"`
	Text    string         `json:"text"`
}
type Extraction struct {
	Chunks       []Chunk  `json:"chunks"`
	QualityFlags []string `json:"quality_flags"`
}

func Extract(ctx context.Context, mediaType string, data []byte) (Extraction, error) {
	if len(data) == 0 || len(data) > 20<<20 {
		return Extraction{}, errors.New("document_size_invalid")
	}
	switch mediaType {
	case "text/csv":
		return extractCSV(data)
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return extractDOCX(data)
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return extractXLSX(data)
	case "application/pdf":
		return extractPDF(ctx, data)
	default:
		return Extraction{}, errors.New("document_type_unsupported")
	}
}

func splitText(text string, locator func(int) map[string]any) ([]Chunk, error) {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "�"))
	if text == "" {
		return nil, errors.New("document_text_empty")
	}
	var chunks []Chunk
	for i, start := 0, 0; start < len(text); i++ {
		end := start + 4000
		if end > len(text) {
			end = len(text)
		}
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		chunks = append(chunks, Chunk{Locator: locator(i), Text: strings.TrimSpace(text[start:end])})
		start = end
		if len(chunks) > maxChunks {
			return nil, errors.New("document_too_many_chunks")
		}
	}
	return chunks, nil
}

func extractCSV(data []byte) (Extraction, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	var chunks []Chunk
	for row := 1; ; row++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Extraction{}, errors.New("csv_invalid")
		}
		text := strings.TrimSpace(strings.Join(record, " | "))
		if text == "" {
			continue
		}
		chunks = append(chunks, Chunk{Locator: map[string]any{"kind": "csv_row", "row": row}, Text: text})
		if len(chunks) > maxChunks {
			return Extraction{}, errors.New("document_too_many_chunks")
		}
	}
	if len(chunks) == 0 {
		return Extraction{}, errors.New("document_text_empty")
	}
	return Extraction{Chunks: chunks}, nil
}

func openArchive(data []byte) (*zip.Reader, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("office_archive_invalid")
	}
	var total uint64
	for _, f := range r.File {
		total += f.UncompressedSize64
		if total > maxArchiveBytes {
			return nil, errors.New("office_archive_too_large")
		}
	}
	return r, nil
}
func archiveFile(r *zip.Reader, name string) ([]byte, error) {
	for _, f := range r.File {
		if f.Name == name {
			rc, e := f.Open()
			if e != nil {
				return nil, e
			}
			defer rc.Close()
			data, readErr := io.ReadAll(io.LimitReader(rc, maxExtractedBytes+1))
			if readErr != nil {
				return nil, readErr
			}
			if len(data) > maxExtractedBytes {
				return nil, errors.New("office_part_too_large")
			}
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

type xmlText struct {
	Text string `xml:",chardata"`
}

func extractDOCX(data []byte) (Extraction, error) {
	r, e := openArchive(data)
	if e != nil {
		return Extraction{}, e
	}
	raw, e := archiveFile(r, "word/document.xml")
	if e != nil {
		return Extraction{}, errors.New("docx_document_missing")
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	paragraph := 0
	var current strings.Builder
	var chunks []Chunk
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return Extraction{}, errors.New("docx_xml_invalid")
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Local == "t" {
				var x xmlText
				if dec.DecodeElement(&x, &v) == nil {
					current.WriteString(x.Text)
				}
			}
		case xml.EndElement:
			if v.Name.Local == "p" {
				paragraph++
				text := strings.TrimSpace(current.String())
				current.Reset()
				if text != "" {
					chunks = append(chunks, Chunk{Locator: map[string]any{"kind": "docx_paragraph", "paragraph": paragraph}, Text: text})
				}
				if len(chunks) > maxChunks {
					return Extraction{}, errors.New("document_too_many_chunks")
				}
			}
		}
	}
	if len(chunks) == 0 {
		return Extraction{}, errors.New("document_text_empty")
	}
	return Extraction{Chunks: chunks}, nil
}

type sharedStrings struct {
	Values []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}
type worksheetCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline string `xml:"is>t"`
}

func extractXLSX(data []byte) (Extraction, error) {
	r, e := openArchive(data)
	if e != nil {
		return Extraction{}, e
	}
	var shared []string
	if raw, e := archiveFile(r, "xl/sharedStrings.xml"); e == nil {
		var s sharedStrings
		if xml.Unmarshal(raw, &s) != nil {
			return Extraction{}, errors.New("xlsx_shared_strings_invalid")
		}
		for _, v := range s.Values {
			var b strings.Builder
			b.WriteString(v.Text)
			for _, run := range v.Runs {
				b.WriteString(run.Text)
			}
			shared = append(shared, b.String())
		}
	}
	var sheetFiles []*zip.File
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	if len(sheetFiles) > maxSpreadsheetSheets {
		return Extraction{}, errors.New("xlsx_too_many_sheets")
	}

	var chunks []Chunk
	totalCells := 0
	for sheetIndex, f := range sheetFiles {
		raw, readErr := archiveFile(r, f.Name)
		if readErr != nil {
			return Extraction{}, readErr
		}
		dec := xml.NewDecoder(bytes.NewReader(raw))
		rowNumber := 0
		var cells []string
		var firstRef, lastRef string
		for {
			token, decodeErr := dec.Token()
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				return Extraction{}, errors.New("xlsx_sheet_invalid")
			}
			switch value := token.(type) {
			case xml.StartElement:
				switch value.Name.Local {
				case "row":
					rowNumber = 0
					for _, attribute := range value.Attr {
						if attribute.Name.Local == "r" {
							rowNumber, _ = strconv.Atoi(attribute.Value)
						}
					}
					cells = cells[:0]
					firstRef, lastRef = "", ""
				case "c":
					var cell worksheetCell
					if dec.DecodeElement(&cell, &value) != nil {
						return Extraction{}, errors.New("xlsx_sheet_invalid")
					}
					totalCells++
					if totalCells > maxSpreadsheetCells {
						return Extraction{}, errors.New("xlsx_too_many_cells")
					}
					cellValue := cell.Value
					if cell.Type == "s" {
						index, conversionErr := strconv.Atoi(cellValue)
						if conversionErr == nil && index >= 0 && index < len(shared) {
							cellValue = shared[index]
						}
					} else if cell.Type == "inlineStr" {
						cellValue = cell.Inline
					}
					if strings.TrimSpace(cellValue) != "" {
						if firstRef == "" {
							firstRef = cell.Ref
						}
						lastRef = cell.Ref
						cells = append(cells, cell.Ref+"="+cellValue)
					}
				}
			case xml.EndElement:
				if value.Name.Local == "row" && len(cells) > 0 {
					chunks = append(chunks, Chunk{
						Locator: map[string]any{"kind": "xlsx_range", "sheet": sheetIndex + 1, "range": firstRef + ":" + lastRef, "row": rowNumber},
						Text:    strings.Join(cells, " | "),
					})
					if len(chunks) > maxChunks {
						return Extraction{}, errors.New("document_too_many_chunks")
					}
				}
			}
		}
	}
	if len(chunks) == 0 {
		return Extraction{}, errors.New("document_text_empty")
	}
	return Extraction{Chunks: chunks}, nil
}

func splitPDFText(text string) ([]Chunk, error) {
	var chunks []Chunk
	for pageIndex, pageText := range strings.Split(text, "\f") {
		if strings.TrimSpace(pageText) == "" {
			continue
		}
		pageChunks, err := splitText(pageText, func(chunkIndex int) map[string]any {
			return map[string]any{"kind": "pdf_page", "page": pageIndex + 1, "chunk": chunkIndex + 1}
		})
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, pageChunks...)
		if len(chunks) > maxChunks {
			return nil, errors.New("document_too_many_chunks")
		}
	}
	if len(chunks) == 0 {
		return nil, errors.New("document_text_empty")
	}
	return chunks, nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("document_text_too_large")
	}
	return data, nil
}

func parsePDFPageCount(info []byte) (int, error) {
	for _, line := range strings.Split(string(info), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "Pages") {
			continue
		}
		pages, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && pages > 0 {
			return pages, nil
		}
		break
	}
	return 0, errors.New("pdf_page_count_invalid")
}

func pdfPageCount(ctx context.Context, input string) (int, error) {
	info, err := exec.CommandContext(ctx, "pdfinfo", input).Output()
	if err != nil || len(info) > 1<<20 {
		return 0, errors.New("pdf_invalid_or_encrypted")
	}
	return parsePDFPageCount(info)
}

func validatePDFPageLimit(pages int, ocr bool) error {
	if pages <= 0 || pages > maxPDFTextPages {
		return errors.New("pdf_page_limit")
	}
	if ocr && pages > maxPDFOCRPages {
		return errors.New("pdf_ocr_page_limit")
	}
	return nil
}

func extractPDF(ctx context.Context, data []byte) (Extraction, error) {
	dir, e := os.MkdirTemp("", "livetranslate-pdf-")
	if e != nil {
		return Extraction{}, errors.New("pdf_temp_failed")
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source.pdf")
	output := filepath.Join(dir, "text.txt")
	if os.WriteFile(input, data, 0600) != nil {
		return Extraction{}, errors.New("pdf_temp_failed")
	}
	pages, pageErr := pdfPageCount(ctx, input)
	if pageErr != nil {
		return Extraction{}, pageErr
	}
	if limitErr := validatePDFPageLimit(pages, false); limitErr != nil {
		return Extraction{}, limitErr
	}
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", input, output)
	if e = cmd.Run(); e != nil {
		return Extraction{}, errors.New("pdf_invalid_or_encrypted")
	}
	text, readErr := readLimitedFile(output, maxExtractedBytes)
	if readErr != nil {
		if readErr.Error() == "document_text_too_large" {
			return Extraction{}, readErr
		}
		return Extraction{}, errors.New("pdf_text_failed")
	}
	flags := []string{}
	if strings.TrimSpace(string(text)) == "" {
		if limitErr := validatePDFPageLimit(pages, true); limitErr != nil {
			return Extraction{}, limitErr
		}
		flags = append(flags, "ocr_required")
		prefix := filepath.Join(dir, "page")
		if exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "200", input, prefix).Run() != nil {
			return Extraction{}, errors.New("pdf_ocr_render_failed")
		}
		images, _ := filepath.Glob(prefix + "-*.png")
		if len(images) != pages {
			return Extraction{}, errors.New("pdf_ocr_render_incomplete")
		}
		var b strings.Builder
		for index, image := range images {
			out, e := exec.CommandContext(ctx, "tesseract", image, "stdout", "-l", "eng+ind").Output()
			if e != nil {
				return Extraction{}, errors.New("pdf_ocr_failed")
			}
			b.Write(out)
			if index+1 < len(images) {
				b.WriteByte('\f')
			}
			if b.Len() > maxExtractedBytes {
				return Extraction{}, errors.New("document_text_too_large")
			}
		}
		text = []byte(b.String())
		flags = append(flags, "ocr_used")
	}
	chunks, e := splitPDFText(string(text))
	if e != nil {
		return Extraction{}, e
	}
	return Extraction{Chunks: chunks, QualityFlags: flags}, nil
}

func (e Extraction) Validate() error {
	if len(e.Chunks) == 0 || len(e.Chunks) > maxChunks {
		return fmt.Errorf("invalid extraction chunk count")
	}
	return nil
}
