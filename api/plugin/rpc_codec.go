package plugin

import "google.golang.org/protobuf/proto"

func (m *Job) GobEncode() ([]byte, error) {
	return proto.Marshal(m)
}

func (m *Job) GobDecode(data []byte) error {
	return proto.Unmarshal(data, m)
}

func (m *JobResponse) GobEncode() ([]byte, error) {
	return proto.Marshal(m)
}

func (m *JobResponse) GobDecode(data []byte) error {
	return proto.Unmarshal(data, m)
}
