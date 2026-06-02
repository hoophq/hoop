(ns webapp.resources.federation.views.test-dialog
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Text]]
   ["lucide-react" :refer [CheckCircle Info XCircle]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn- valid-email? [email]
  (and (string? email)
       (not (str/blank? email))
       (re-matches #"^[^@\s]+@[^@\s]+\.[^@\s]+$" email)))

(defn- result-row [label value]
  [:> Box {:class "space-y-0.5"}
   [:> Text {:size "1" :class "text-[--gray-10] uppercase tracking-wide block"} label]
   [:> Text {:size "2" :class "font-mono text-[--gray-12] break-all block"} value]])

(defn- test-success-display [test-result token-ttl-seconds email]
  [:> Box {:class "space-y-3"}
   [:> Flex {:align "center" :gap "2"}
    [:> CheckCircle {:size 18 :class "text-[--green-11] flex-shrink-0"}]
    [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
     (str "Session would start as " email)]]

   [:> Box {:class "rounded-lg border border-[--gray-6] p-4 space-y-3"}
    [result-row "Resolved principal" (:resolved_principal test-result)]
    [result-row "Impersonated via" (:admin_principal test-result)]
    [result-row "Token TTL" (str (quot (or token-ttl-seconds 3600) 60) " minutes")]
    [result-row "Env vars emitted" (str/join ", " (:env_var_keys test-result))]]])

(defn- test-error-display [test-result]
  (let [error-str (or (:error test-result) (:message test-result) (str test-result))
        probe-status (:probe_status test-result)]
    [:> Box {:class "rounded-lg border border-[--red-6] bg-[--red-2] overflow-hidden"}
     [:> Flex {:align "center" :gap "2" :class "px-3 py-2.5 bg-[--red-3]"}
      [:> XCircle {:size 16 :class "text-[--red-11] flex-shrink-0"}]
      [:> Text {:size "2" :weight "medium" :class "text-[--red-12] leading-none"} "Test failed"]]

     ;; left padding aligns the body under the title text, past the icon
     [:> Box {:class "px-3 py-3 pl-[2.375rem] space-y-2"}
      (when probe-status
        [:> Text {:size "1" :as "p" :class "text-[--red-11]"}
         "Probe status: "
         [:span {:class "font-mono"} probe-status]])

      [:> Box {:class "p-2 bg-[--gray-1] border border-[--red-6] rounded font-mono text-xs text-[--gray-12] whitespace-pre-wrap break-all max-h-48 overflow-auto"}
       error-str]]]))

(defn main [{:keys [conn-data]}]
  (r/with-let [current-user (rf/subscribe [:users->current-user])
               form-sub (rf/subscribe [:federation/form])
               test-status-sub (rf/subscribe [:federation/test-status])
               test-result-sub (rf/subscribe [:federation/test-result])
               email (r/atom nil)]

    (when (and (nil? @email)
               (get-in @current-user [:data :email]))
      (reset! email (get-in @current-user [:data :email])))

    (fn []
      (let [test-status @test-status-sub
            test-result @test-result-sub
            form @form-sub
            loading? (= test-status :loading)
            success? (= test-status :success)
            project-id (get-in form [:extra_config :project_id])
            target-template (:identity_target_template form)
            ready-to-run? (and (valid-email? @email)
                               (not (str/blank? project-id))
                               (not (str/blank? target-template))
                               (not loading?))

            close-modal #(do (rf/dispatch [:federation/reset-test])
                             (rf/dispatch [:modal->close]))

            run-test #(rf/dispatch [:federation/test @email conn-data])]

        [:> Box {:class "space-y-5 p-1"}
         [:> Box {:class "space-y-1"}
          [:> Heading {:size "5" :weight "bold" :class "text-[--gray-12]"}
           "Test as user"]
          [:> Text {:size "2" :class "text-[--gray-11]"}
           "Run the federation hook as a specific Hoop user and inspect what the session would receive."]]

         (if success?
           [test-success-display test-result (:token_ttl_seconds form) @email]

           [:<>
            [forms/input
             {:label "Hoop user"
              :placeholder "user@example.com"
              :type "email"
              :required true
              :not-margin-bottom? true
              :value (or @email "")
              :on-change #(reset! email (-> % .-target .-value))}]

            [:> Callout.Root {:color "blue" :variant "soft" :size "1" :class "items-center"}
             [:> Callout.Icon [:> Info {:size 14}]]
             [:> Callout.Text {:size "1"}
              "This is a dry run. No session is opened, no audit record is created."]]

            (case test-status
              :loading
              [:> Flex {:align "center" :gap "2" :class "text-[--gray-11]"}
               [:span {:class "inline-block w-4 h-4 border-2 border-[--gray-8] border-t-transparent rounded-full animate-spin"}]
               [:> Text {:size "2"} "Running test…"]]

              :error
              [test-error-display test-result]

              nil)])

         [:> Flex {:justify "end" :gap "2"}
          (if success?
            [:<>
             [:> Button {:variant "soft"
                         :color "gray"
                         :type "button"
                         :on-click #(rf/dispatch [:federation/reset-test])}
              "Run again"]
             [:> Button {:variant "solid"
                         :type "button"
                         :on-click close-modal}
              "Done"]]

            [:<>
             [:> Button {:variant "soft"
                         :color "gray"
                         :type "button"
                         :on-click close-modal}
              "Cancel"]
             [:> Button {:variant "solid"
                         :type "button"
                         :disabled (not ready-to-run?)
                         :on-click run-test}
              "Run test"]])]]))))
