export class Observable {
    constructor() {
        this.subscribers = [];
    }

    subscribers = [];

    subscribe(cb) {
        this.subscribers.push(cb);
    }

    publish(value) {
        for (const cb of this.subscribers) {
            cb(value);
        }
    }
}
export default Observable;