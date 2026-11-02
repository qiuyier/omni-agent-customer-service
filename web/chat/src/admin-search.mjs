// Return original positions; filtering must never replace the editable draft.
export function matchingFAQIndices(entries, query) {
 const needle=query.trim().toLowerCase();
 return entries.flatMap((entry,index)=>!needle||[entry.title,entry.content,...entry.keywords].some(value=>value.toLowerCase().includes(needle))?[index]:[]);
}
