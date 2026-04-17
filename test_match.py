"""
Live end-to-end matchmaking test for elo-service.fly.dev.

USAGE:
  python test_match.py

WHAT IT DOES:
  Connects two players (one registered user, one guest) to the matchmaking
  queue for the "example" game simultaneously. When both are queued, the
  matchmaking worker pairs them, provisions a Hetzner host VM (cold start:
  ~2 min), starts a game container, and sends a match_ready message to both
  WebSocket connections.

KEY API FACTS:
  - Login:       POST /user/login          { email, password }
  - Guest token: POST /guest/login         { displayName }
  - Join queue:  GET  /match/join?gameID=<id>   (WebSocket, Authorization header)
  - Queue param is camelCase "gameID", NOT "game_id"

TOKENS:
  Tokens are short-lived JWTs. If you get 401/expired errors, re-run the
  login/guest-login curls below and paste fresh values into TOKEN_P1/TOKEN_P2.

  Login:
    curl -s -X POST https://elo-service.fly.dev/user/login \\
      -H "Content-Type: application/json" \\
      -d '{"email":"andyhudson725@gmail.com","password":"basewars432!"}'

  Guest:
    curl -s -X POST https://elo-service.fly.dev/guest/login \\
      -H "Content-Type: application/json" \\
      -d '{"displayName":"bot1"}'

GAME IDs (staging DB):
  example    b2b8f32d-763e-4a63-b1ec-121a65e376f2  (port 8080, lobby 2)
  Battle Bots  ed4ec9a6-b4ee-42c5-8feb-031e042bca8c  (port 7777, lobby 2)
"""
import asyncio
import json
import websockets

BASE_WS = "wss://elo-service.fly.dev"
GAME_ID = "b2b8f32d-763e-4a63-b1ec-121a65e376f2"

TOKEN_P1 = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiMDFkOWRhYmYtYTAyYi00OGFiLTljMWEtZDdlODU0YjgzNWFkIiwiZGlzcGxheV9uYW1lIjoidGV0cjQiLCJ1c2VybmFtZSI6InRldHI0IiwiaW1wZXJzb25hdGlvbl9pZCI6IiIsImV4cCI6MTc3NjQ5MjMzNX0.lerPmjxIRcT_1no3E1R89_MT1yfuE0Z2KZ94czOFbcI"
TOKEN_P2 = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpZCI6ImdfMDE3ZGU0MTgtYzQwZS00NDUwLWIzZjctMzk4OGE0ZGUxYTRiIiwiZGlzcGxheV9uYW1lIjoiYm90MSIsImV4cCI6MTc3NjQ5MjM3N30.it3FQxeg_7TMbd6Lt_BOxi4sJiZvfN_0EoBbIofL84c"

async def join_queue(label: str, token: str, results: dict):
    url = f"{BASE_WS}/match/join?gameID={GAME_ID}"
    headers = {"Authorization": f"Bearer {token}"}
    print(f"[{label}] Connecting to {url}")
    try:
        async with websockets.connect(
            url, additional_headers=headers, open_timeout=30,
            ping_interval=None, close_timeout=10,  # server sends "searching" keepalives; no client pings needed
        ) as ws:
            print(f"[{label}] Connected, waiting for match...")
            async for raw in ws:
                try:
                    msg = json.loads(raw)
                except Exception:
                    msg = raw
                print(f"[{label}] << {json.dumps(msg, indent=2) if isinstance(msg, dict) else msg}")
                results[label] = msg
    except Exception as e:
        print(f"[{label}] Error: {e}")
        results[label] = {"error": str(e)}

async def main():
    results = {}
    print("Starting matchmaking test — connecting both players simultaneously...")
    await asyncio.gather(
        join_queue("P1_tetr4", TOKEN_P1, results),
        join_queue("P2_bot1",  TOKEN_P2, results),
    )
    print("\n=== Final results ===")
    for k, v in results.items():
        print(f"{k}: {v}")

if __name__ == "__main__":
    asyncio.run(main())
