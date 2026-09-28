import http from 'k6/http';
import { check } from 'k6';

export const options = {
    scenarios: {
        redirect_baseline: {
            executor: 'constant-vus',
            vus: 50,
            duration: '30s',
        },
    },
    thresholds: {
        http_req_failed: ['rate<0.01'],
        http_req_duration: ['p(95)<100'],
    },
};

export default function () {
    const response = http.get('http://localhost:8080/Q', {
        redirects: 0,
    });

    check(response, {
        'status is 302': (r) => r.status === 302,
        'location is correct': (r) =>
            r.headers['Location'] === 'https://collision.example',
    });
}