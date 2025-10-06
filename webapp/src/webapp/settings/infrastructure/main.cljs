(ns webapp.settings.infrastructure.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Switch Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.config :as config]))

(defn main []
  (let [infrastructure-config (rf/subscribe [:infrastructure->config])
        min-loading-done (r/atom false)]

    (rf/dispatch [:infrastructure->get-config])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @infrastructure-config))
                         (not @min-loading-done))
            submitting? (:submitting? @infrastructure-config)
            data (:data @infrastructure-config)]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          :else
          [:> Box {:class "min-h-screen bg-gray-1"}
           ;; Header with Save button
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"} "Infrastructure"]
             [:> Button {:size "3"
                         :loading submitting?
                         :disabled submitting?
                         :on-click #(rf/dispatch [:infrastructure->save-config])}
              "Save"]]]

           ;; Content
           [:> Box {:class "rounded-lg p-radix-7"}
            [:> Box {:class "space-y-radix-9"}

             ;; Product analytics section
             [:> Grid {:columns "7" :gap "7"}
              [:> Box {:grid-column "span 2 / span 2"}
               [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                "Product analytics"]
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "Help us improve Hoop by sharing usage data. Access and resources information are not collected."]]

              [:> Box {:class "space-y-radix-4" :grid-column "span 5 / span 5"}
               [:> Flex {:align "center" :gap "3"}
                [:> Switch {:checked (:analytics-enabled data)
                            :onCheckedChange #(rf/dispatch [:infrastructure->toggle-analytics %])}]
                [:> Text {:size "3" :weight "medium"}
                 (if (:analytics-enabled data) "On" "Off")]]]]

             ;; gRPC configuration section
             [:> Grid {:columns "7" :gap "7"}
              [:> Box {:grid-column "span 2 / span 2"}
               [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                "gRPC configuration"]
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "Specify the gRPC endpoint URL for establishing secure connections between Hoop agents and your gateway infrastructure."]

               [callout-link/main {:href (get-in config/docs-url [:clients :command-line :managing-configuration])
                                   :text "Learn more about gRPC"}]]

              [:> Box {:grid-column "span 5 / span 5"}
               [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12] mb-1"}
                "gRPC URL"]
               [forms/input
                {:placeholder "e.g. grpcs://yourgateway-domain.tld:443"
                 :value (:grpc-url data)
                 :on-change #(rf/dispatch [:infrastructure->update-field
                                           :grpc-url (-> % .-target .-value)])}]]]

             [:> Grid {:columns "7" :gap "7"}
              [:> Box {:grid-column "span 2 / span 2"}
               [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                "PostgreSQL Proxy Port"]
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "Organization-wide default for local PostgreSQL proxy port forwarding."]]

              [:> Box {:grid-column "span 5 / span 5"}
               [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12] mb-1"}
                "Proxy Port"]
               [forms/input
                {:placeholder "e.g. 5432"
                 :value (:postgres-proxy-port data)
                 :on-change #(rf/dispatch [:infrastructure->update-field
                                           :postgres-proxy-port (-> % .-target .-value)])}]]]

             [:> Grid {:columns "7" :gap "7"}
              [:> Box {:grid-column "span 2 / span 2"}
               [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                "RDP Proxy Port"]
               [:> Text {:size "3" :class "text-[--gray-11]"}
                "Organization-wide default for local Remote Desktop Protocol proxy port forwarding."]]

              [:> Box {:grid-column "span 5 / span 5"}
               [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12] mb-1"}
                "Proxy Port"]
               [forms/input
                {:placeholder "e.g. 3389"
                 :value (:rdp-proxy-port data)
                 :on-change #(rf/dispatch [:infrastructure->update-field
                                           :rdp-proxy-port (-> % .-target .-value)])}]]]]]])))))
