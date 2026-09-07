// One request in flight plus one follow-up. Keep the whole refresh (including
// activity) ordered so late responses cannot overwrite a newer task snapshot.
export class CoalescedRefresh {
  private running = false;
  private pending = false;
  private result: Promise<void> = Promise.resolve();

  private readonly refresh: () => Promise<void>;
  constructor(refresh: () => Promise<void>) { this.refresh = refresh; }

  request(): Promise<void> {
    this.pending = true;
    if (!this.running) {
      this.running = true;
      this.result = Promise.resolve().then(async () => {
        try {
          while (this.pending) {
            this.pending = false;
            await this.refresh();
          }
        } finally {
          this.running = false;
        }
      });
    }
    return this.result;
  }
}
