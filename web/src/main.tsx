import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "./global.css";

import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { exchangeToken } from "./api/client";
import { App } from "./App";
import { theme } from "./theme";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: false, retry: 1, staleTime: 5_000 },
  },
});

exchangeToken().finally(() => {
  // A data router (the app's own routes are inside App), so pages can hold
  // on to unsaved work when the user navigates away.
  const router = createBrowserRouter([{ path: "*", element: <App /> }]);
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <MantineProvider theme={theme} defaultColorScheme="dark">
        <Notifications position="bottom-right" limit={4} />
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </MantineProvider>
    </StrictMode>,
  );
});
