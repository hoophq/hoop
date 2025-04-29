(ns webapp.features.runbooks.views.runbook-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Button Heading Grid]]
   ["@heroicons/react/24/outline" :as hero-outline]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn back-button []
  [:a {:class "inline-flex items-center text-sm text-gray-600 mb-6 hover:text-gray-900"
       :on-click #(rf/dispatch [:navigate :runbooks])}
   [:> hero-outline/ArrowLeftIcon {:class "h-4 w-4 mr-1"}]
   "Back"])

(defn edit-form [params]
  (let [path-id (:path-id params)
        connection-id (:connection-id params)
        initial-path (r/atom "")
        path (r/atom (or path-id ""))
        path-loaded (r/atom false)
        is-submitting (r/atom false)
        scroll-pos (r/atom 0)
        paths-by-connection (rf/subscribe [:runbooks/paths-by-connection])]

    (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])
    (when connection-id
      (rf/dispatch [:connections->get-connection-details connection-id]))

    (fn []
      (let [adding-new-path? (and connection-id (not path-id))
            is-existing-path? (boolean path-id)
            connection-paths (get @paths-by-connection connection-id)]

        (when (and (not @path-loaded)
                   adding-new-path?
                   (seq connection-paths))
          (reset! path-loaded true)
          (reset! initial-path (first connection-paths))
          (reset! path (first connection-paths)))

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (reset! is-submitting true)
                              (cond
                                adding-new-path?
                                (do
                                  (rf/dispatch [:runbooks/add-path-to-connection
                                                {:path @path
                                                 :connection-id connection-id}])
                                  (js/setTimeout
                                   #(do
                                      (rf/dispatch [:navigate :runbooks])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Path "
                                                                (if (empty? @path)
                                                                  "removed"
                                                                  (str "'" @path "' added"))
                                                                " successfully!")}]))
                                   1000))

                                is-existing-path?
                                (do
                                  (rf/dispatch [:runbooks/delete-path path-id])
                                  (js/setTimeout
                                   #(do
                                      (rf/dispatch [:navigate :runbooks])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Path '" path-id "' deleted successfully!")}]))
                                   1000))))}

          [:div
           [:> Flex {:p "5" :gap "2"}
            [back-button]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between"
                      :align "center"}
             [:> Heading {:as "h2" :size "8"}
              "Configure Runbooks"]
             [:> Flex {:gap "5" :align "center"}
              [:> Button {:size "3"
                          :loading @is-submitting
                          :disabled @is-submitting
                          :type "submit"}
               "Save"]]]]]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Set Runbooks path"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Used to access git repository. No path will consider root path as default."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [forms/input
              {:placeholder "e.g. /path/to/runbooks"
               :label "Path"
               :value @path
               :required false
               :class "w-full"
               :autoFocus (not is-existing-path?)
               :disabled (or @is-submitting is-existing-path?)
               :on-change #(reset! path (-> % .-target .-value))}]]]]]]))))

(defn main [mode & [params]]
  [edit-form params])
