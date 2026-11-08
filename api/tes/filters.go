package tes

// GetTags pairs tag keys with values. A key without a value matches any value.
func (x *ListTasksRequest) GetTags() map[string]string {
	if x.TagKey == nil || len(x.TagKey) == 0 {
		return nil
	}
	out := map[string]string{}
	for i := 0; i < len(x.TagKey); i++ {
		if i < len(x.TagValue) {
			out[x.TagKey[i]] = x.TagValue[i]
		} else {
			out[x.TagKey[i]] = ""
		}
	}
	return out
}
