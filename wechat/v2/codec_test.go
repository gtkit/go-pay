package v2

import "testing"

func TestEncodeDecodeXMLRoundTrip(t *testing.T) {
	params := map[string]string{
		"appid":        "wx123",
		"body":         "测试商品 & <特殊字符>",
		"out_trade_no": "ORD-1",
		"total_fee":    "100",
	}
	data, err := encodeXML(params)
	if err != nil {
		t.Fatalf("encodeXML error = %v", err)
	}

	got, err := decodeXML(data)
	if err != nil {
		t.Fatalf("decodeXML error = %v", err)
	}
	for k, v := range params {
		if got[k] != v {
			t.Errorf("decoded[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestDecodeXMLCDATA(t *testing.T) {
	data := []byte(`<xml><return_code><![CDATA[SUCCESS]]></return_code><out_trade_no><![CDATA[ORD-2]]></out_trade_no></xml>`)
	got, err := decodeXML(data)
	if err != nil {
		t.Fatalf("decodeXML error = %v", err)
	}
	if got["return_code"] != "SUCCESS" || got["out_trade_no"] != "ORD-2" {
		t.Fatalf("decodeXML = %+v", got)
	}
}

func TestDecodeXMLNonXMLRoot(t *testing.T) {
	// 退款明文根元素为 <root>，应同样可解析
	data := []byte(`<root><refund_status>SUCCESS</refund_status></root>`)
	got, err := decodeXML(data)
	if err != nil {
		t.Fatalf("decodeXML error = %v", err)
	}
	if got["refund_status"] != "SUCCESS" {
		t.Fatalf("decodeXML = %+v", got)
	}
}

func TestDecodeXMLEmpty(t *testing.T) {
	if _, err := decodeXML([]byte("")); err == nil {
		t.Fatal("decodeXML(empty) error = nil, want error")
	}
}
