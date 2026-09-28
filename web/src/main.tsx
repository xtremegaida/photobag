import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "./global.css";

import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { exchangeToken } from "./api/client";
import { App } from "./App";
import { theme } from "./theme";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: false, retry: 1, staleTime: 5_000 },
  },
});

exchangeToken().finally(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <MantineProvider theme={theme} defaultColorScheme="dark">
        <Notifications position="bottom-right" limit={4} />
        <QueryClientProvider client={queryClient}>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </QueryClientProvider>
      </MantineProvider>
    </StrictMode>,
  );
});
