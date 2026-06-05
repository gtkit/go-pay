package v2

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
)

// encodeXML 将参数表序列化为微信 v2 的 <xml> 报文。
//
// 元素按参数名排序输出（顺序不影响微信解析，仅为结果稳定可测），
// 值经 XML 转义，避免商品描述等字段中的特殊字符破坏报文。
func encodeXML(params map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b bytes.Buffer
	b.WriteString("<xml>")
	for _, k := range keys {
		b.WriteByte('<')
		b.WriteString(k)
		b.WriteByte('>')
		if err := xml.EscapeText(&b, []byte(params[k])); err != nil {
			return nil, fmt.Errorf("escape xml value for %q: %w", k, err)
		}
		b.WriteString("</")
		b.WriteString(k)
		b.WriteByte('>')
	}
	b.WriteString("</xml>")
	return b.Bytes(), nil
}

// decodeXML 解析微信 v2 报文为参数表。
//
// 解析根元素（如 <xml> 或退款明文的 <root>）下的一层子元素，子元素文本
// （含 CDATA）作为字符串值；CharData 可能分片，故对同名元素累加文本。
// 基于嵌套深度判定，不依赖根元素名。
func decodeXML(data []byte) (map[string]string, error) {
	m := make(map[string]string)
	dec := xml.NewDecoder(bytes.NewReader(data))

	depth := 0
	var current string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				current = t.Name.Local
			}
		case xml.CharData:
			if depth == 2 && current != "" {
				m[current] += string(t)
			}
		case xml.EndElement:
			if depth == 2 {
				current = ""
			}
			depth--
		}
	}

	if len(m) == 0 {
		return nil, fmt.Errorf("decode xml: empty or invalid document")
	}
	return m, nil
}
