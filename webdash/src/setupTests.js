globalThis.IS_REACT_ACT_ENVIRONMENT = true;
globalThis.fetch = async (input) => {
  if (input === '/login/token') {
    return { status: 200, text: async () => '' };
  }
  return { json: async () => ({ tasks: [] }) };
};
