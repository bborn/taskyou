const copyButton = document.querySelector('.command');
copyButton.addEventListener('click', async () => {
  const command = copyButton.querySelector('code');
  const label = copyButton.querySelector('.copy');
  const status = document.querySelector('#copy-status');
  try {
    await navigator.clipboard.writeText(command.textContent);
    label.textContent = 'Copied';
    status.textContent = 'Install command copied.';
  } catch {
    const range = document.createRange();
    range.selectNodeContents(command);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    label.textContent = 'Select';
    status.textContent = 'Select and copy the highlighted install command.';
  }
});
